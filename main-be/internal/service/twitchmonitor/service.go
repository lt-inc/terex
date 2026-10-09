// Package twitchmonitor builds bounded, read-only snapshots for Grafana. Failed
// reads preserve the previous timestamp so missing data never appears as zero.
package twitchmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"terex/main-be/internal/domain"
	"time"
)

type Diagnostics interface {
	Diagnostics(context.Context, string) (domain.TwitchFlowDiagnostics, error)
}
type Service struct {
	Repo        domain.TwitchMonitoringRepo
	Flows       domain.TwitchFlowRepo
	Diagnostics Diagnostics
	Collector   domain.TwitchCollectorMonitoring
	AppURL      string
}

func (s *Service) Tick(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	now := time.Now().UTC()
	work, err := s.Repo.TwitchRuntimeWork(ctx, now)
	if err != nil {
		return err
	}
	flows, err := s.Flows.ListTwitchFlows(ctx)
	if err != nil {
		return err
	}
	status, err := s.Collector.Status(ctx)
	if err != nil {
		return fmt.Errorf("monitor collector status: %w", err)
	}
	if status.Runtime == nil || status.Runtime.Epoch != work.Epoch {
		return errors.New("collector monitoring epoch is unavailable or changed")
	}
	work.Counts = append(work.Counts, status.Runtime.Counts...)
	work.ActiveTotal += status.Runtime.ActiveTotal
	work.ActiveTasks = append(work.ActiveTasks, status.Runtime.ActiveTasks...)
	sort.SliceStable(work.ActiveTasks, func(i, j int) bool {
		a, b := work.ActiveTasks[i], work.ActiveTasks[j]
		if (a.StartedAt != nil) != (b.StartedAt != nil) {
			return a.StartedAt != nil
		}
		if a.UpdatedAt != nil && b.UpdatedAt != nil && !a.UpdatedAt.Equal(*b.UpdatedAt) {
			return a.UpdatedAt.After(*b.UpdatedAt)
		}
		return a.ClipID < b.ClipID
	})
	if len(work.ActiveTasks) > 100 {
		work.ActiveTasks = work.ActiveTasks[:100]
	}
	for i := range work.ActiveTasks {
		if strings.HasPrefix(work.ActiveTasks[i].TerexURL, "/") {
			work.ActiveTasks[i].TerexURL = strings.TrimRight(s.AppURL, "/") + work.ActiveTasks[i].TerexURL
		}
	}
	out := domain.TwitchMonitoringSnapshot{CollectorStatus: status, Counts: work.Counts, ActiveTasks: work.ActiveTasks, ActiveTotal: work.ActiveTotal, Discovery: []json.RawMessage{}}
	var failures []error
	for _, flow := range flows {
		if flow.Archived {
			continue
		}
		d, e := s.Diagnostics.Diagnostics(ctx, flow.ID)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		collector, e := s.Collector.Diagnostics(ctx, flow.ID)
		if e != nil {
			failures = append(failures, e)
			continue
		}
		Enrich(&d, collector)
		for _, r := range collector.Discovery {
			raw, _ := json.Marshal(struct {
				domain.TwitchDiagnosticDiscovery
				FlowName string `json:"flow_name"`
			}{r, flow.Name})
			out.Discovery = append(out.Discovery, raw)
		}
		payload := struct {
			domain.TwitchFlowDiagnostics
			FlowName string `json:"flow_name"`
		}{d, flow.Name}
		if e = s.Repo.SaveTwitchMonitoring(ctx, "flow", flow.ID, work.Epoch, now, payload); e != nil {
			failures = append(failures, e)
		}
	}
	// A partial sample does not refresh system/current: dashboard freshness makes
	// the gap visible while successful per-flow samples can still advance.
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	status.Runtime = nil
	out.CollectorStatus = status
	if err = s.Repo.SaveTwitchMonitoring(ctx, "system", "current", work.Epoch, now, out); err != nil {
		return err
	}
	if err = s.Repo.CleanupTwitchMonitoring(ctx, work.Epoch); err != nil {
		return err
	}
	slog.DebugContext(ctx, "twitch monitoring snapshot refreshed", "epoch", work.Epoch, "flows", len(flows), "active_total", work.ActiveTotal)
	return nil
}

// Enrich uses the same classifications for API diagnostics and cached panels.
func Enrich(out *domain.TwitchFlowDiagnostics, collector domain.TwitchDiagnosticCollector) {
	pending := map[string]bool{}
	for _, id := range out.PendingClipIDs {
		pending[id] = true
	}
	for _, item := range collector.Clips {
		if !pending[item.ClipID] {
			continue
		}
		state := item.State
		switch state {
		case "downloading", "uploading":
			state = "downloading"
		case "importing", "uploaded", "verifying":
			state = "verifying"
		case "retry_wait":
			state = "retry"
		case "queued", "reserved", "waiting", "waiting_quota", "waiting_capacity", "revalidating":
			state = "queued"
		case "failed", "deferred":
			state = "failed"
		default:
			continue
		}
		if previous := out.PendingStates[item.ClipID]; previous != "" && out.Sources[previous] > 0 {
			out.Sources[previous]--
		}
		out.Sources[state]++
	}
	out.Collector.Available = true
	out.Collector.Discovery = collector.Discovery
	if out.EstimatedAdditionalClips > out.Collector.Remaining && out.Feasibility == "needs_sources" {
		out.Feasibility = "budget_insufficient"
		out.Messages = append(out.Messages, "Оставшегося общего дневного бюджета может не хватить на подборку. Сократите минимальную длину или увеличьте бюджет; оценка не гарантирует доступность подходящих клипов")
	}
}
