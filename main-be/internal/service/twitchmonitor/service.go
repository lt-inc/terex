// Package twitchmonitor builds bounded, read-only snapshots for Grafana. Each
// component publishes its own successful observations; unavailable components
// are identified explicitly and never contribute fabricated zero counts.
package twitchmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

type Diagnostics interface {
	Diagnostics(context.Context, string) (domain.TwitchFlowDiagnostics, error)
}
type Service struct {
	Repo         domain.TwitchMonitoringRepo
	Flows        domain.TwitchFlowRepo
	Diagnostics  Diagnostics
	Collector    domain.TwitchCollectorMonitoring
	AppURL       string
	FlowsPerTick int
	flowMu       sync.Mutex
	nextFlowID   string
}

func (s *Service) Tick(ctx context.Context) error {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	now := time.Now().UTC()
	work, err := s.Repo.TwitchRuntimeWork(ctx, now)
	if err != nil {
		return err
	}
	prepareTasks(work.ActiveTasks, s.AppURL)
	out := domain.TwitchMonitoringSnapshot{
		CollectorStatus: domain.TwitchCollectorStatus{LastError: "Данные main-be обновлены; данные collector ещё не получены"},
		Counts:          work.Counts, ActiveTasks: work.ActiveTasks, ActiveTotal: work.ActiveTotal,
		OldestRenderQueuedAt: work.OldestRenderQueuedAt,
		Discovery:            []json.RawMessage{}, DiagnosticsErrors: []domain.TwitchMonitoringFailure{},
	}
	// Persist the main queues before any remote/expensive diagnostics. Even a
	// subsequent timeout must not hide a successful main runtime observation.
	if err = s.Repo.SaveTwitchMonitoring(ctx, "system", "current", work.Epoch, now, out); err != nil {
		return err
	}
	// Reserve the final five seconds for persisting partial-data explanations.
	// Keep the original parent cancellation: shutdown must still stop writes.
	deadline, _ := ctx.Deadline()
	diagnosticCtx, diagnosticCancel := context.WithDeadline(ctx, deadline.Add(-5*time.Second))
	defer diagnosticCancel()
	var failures []error
	addFailure := func(component, flowID, flowName string, e error) {
		failures = append(failures, fmt.Errorf("monitor %s %s: %w", component, flowID, e))
		out.DiagnosticsErrors = append(out.DiagnosticsErrors, domain.TwitchMonitoringFailure{
			Component: component, FlowID: flowID, FlowName: flowName, Message: e.Error(),
		})
	}
	statusCtx, statusCancel := context.WithTimeout(diagnosticCtx, 3*time.Second)
	status, statusErr := s.Collector.Status(statusCtx)
	statusCancel()
	if statusErr == nil && (status.Runtime == nil || status.Runtime.Epoch != work.Epoch) {
		statusErr = errors.New("collector monitoring epoch is unavailable or changed")
	}
	if statusErr != nil {
		status = domain.TwitchCollectorStatus{LastError: statusErr.Error()}
		addFailure("collector_status", "", "", statusErr)
	} else {
		status.Available = true
		observedAt := time.Now().UTC()
		out.CollectorObservedAt = &observedAt
		out.Counts = append(out.Counts, status.Runtime.Counts...)
		out.ActiveTotal += status.Runtime.ActiveTotal
		out.ActiveTasks = append(out.ActiveTasks, status.Runtime.ActiveTasks...)
		prepareTasks(out.ActiveTasks, s.AppURL)
		if len(out.ActiveTasks) > 100 {
			out.ActiveTasks = out.ActiveTasks[:100]
		}
	}
	status.Runtime = nil
	out.CollectorStatus = status
	if err = s.Repo.SaveTwitchMonitoring(ctx, "system", "current", work.Epoch, now, out); err != nil {
		return errors.Join(append(failures, err)...)
	}
	var flows []twitch.Flow
	if lean, ok := s.Flows.(interface {
		ListTwitchMonitoringFlows(context.Context) ([]twitch.Flow, error)
	}); ok {
		flows, err = lean.ListTwitchMonitoringFlows(diagnosticCtx)
	} else {
		flows, err = s.Flows.ListTwitchFlows(diagnosticCtx)
	}
	if err != nil {
		addFailure("flow_list", "", "", err)
	}
	flows = s.orderedFlows(flows)
	limit := s.FlowsPerTick
	if limit <= 0 {
		limit = 2
	}
	out.FlowsTotal = len(flows)
	out.FlowsPerTick = limit
	attempted, observed := 0, 0
	for i, flow := range flows {
		// Leaving flows for the next minute is intentional. Their independently
		// timestamped snapshots remain unchanged rather than appearing newly read.
		if attempted >= limit {
			break
		}
		if diagnosticCtx.Err() != nil {
			addFailure("flow_batch", flow.ID, flow.Name, fmt.Errorf("В этом цикле не опрошено %d потоков: исчерпан бюджет диагностики; следующий цикл начнёт с этого потока", len(flows)-i))
			break
		}
		attempted++
		out.FlowsAttempted = attempted
		s.flowMu.Lock()
		s.nextFlowID = flows[(i+1)%len(flows)].ID
		out.NextFlowID = s.nextFlowID
		s.flowMu.Unlock()
		func() {
			// Keep main and collector reads sequential with separate deadlines.
			// Rotate next cycle if the total budget cannot cover the whole list.
			flowCtx, flowCancel := context.WithTimeout(diagnosticCtx, 5*time.Second)
			d, e := s.Diagnostics.Diagnostics(flowCtx, flow.ID)
			flowCancel()
			if e != nil {
				addFailure("flow_main", flow.ID, flow.Name, e)
				return
			}
			// Use the actual main observation timestamp, not this cycle's time
			// or a later collector response, for per-flow freshness.
			if d.GeneratedAt.IsZero() {
				d.GeneratedAt = time.Now().UTC()
			}
			d.Collector.Available = false
			d.Collector.ObservedAt = nil
			d.Collector.Message = "Данные main-be обновлены; состояние скачивания collector недоступно"
			if statusErr != nil {
				d.Collector.Message = statusErr.Error()
			}
			payload := struct {
				domain.TwitchFlowDiagnostics
				FlowName string `json:"flow_name"`
			}{d, flow.Name}
			// Save main eligibility/capacity before enriching it with collector data.
			save := func() error {
				// Successful main observations outlive the read deadline. A slow
				// collector response must not prevent their durable publication.
				saveCtx, saveCancel := context.WithTimeout(ctx, 2*time.Second)
				defer saveCancel()
				return s.Repo.SaveTwitchMonitoring(saveCtx, "flow", flow.ID, work.Epoch, d.GeneratedAt, payload)
			}
			if e = save(); e != nil {
				addFailure("flow_save", flow.ID, flow.Name, e)
				return
			}
			observed++
			out.MainFlowsObserved = observed
			if statusErr != nil {
				// One failed status request should not repeat its remote timeout
				// for every flow. Their main diagnostics remain independently fresh.
				return
			}
			collectorCtx, collectorCancel := context.WithTimeout(diagnosticCtx, 3*time.Second)
			collector, e := s.Collector.Diagnostics(collectorCtx, flow.ID)
			collectorCancel()
			if e != nil {
				addFailure("flow_collector", flow.ID, flow.Name, e)
				d.Collector.Message = e.Error()
			} else {
				Enrich(&d, collector)
				observedAt := time.Now().UTC()
				d.Collector.ObservedAt = &observedAt
				for _, r := range collector.Discovery {
					raw, marshalErr := json.Marshal(struct {
						domain.TwitchDiagnosticDiscovery
						FlowName   string    `json:"flow_name"`
						ObservedAt time.Time `json:"observed_at"`
					}{r, flow.Name, observedAt})
					if marshalErr != nil {
						addFailure("flow_discovery", flow.ID, flow.Name, marshalErr)
						continue
					}
					out.Discovery = append(out.Discovery, raw)
				}
			}
			payload.TwitchFlowDiagnostics = d
			if e = save(); e != nil {
				addFailure("flow_save", flow.ID, flow.Name, e)
			}
		}()
		// Publish completed observations/errors progressively. A later pool
		// timeout must not erase every flow result collected earlier this cycle.
		flushCtx, flushCancel := context.WithTimeout(ctx, 2*time.Second)
		err = s.Repo.SaveTwitchMonitoring(flushCtx, "system", "current", work.Epoch, now, out)
		flushCancel()
		if err != nil {
			addFailure("snapshot_save", flow.ID, flow.Name, err)
		}
	}
	if err = s.Repo.SaveTwitchMonitoring(ctx, "system", "current", work.Epoch, now, out); err != nil {
		failures = append(failures, err)
	}
	if err = s.Repo.CleanupTwitchMonitoring(ctx, work.Epoch); err != nil {
		failures = append(failures, err)
	}
	slog.InfoContext(ctx, "twitch monitoring snapshot refreshed", "epoch", work.Epoch, "flows", len(flows), "flows_per_tick", limit, "flows_attempted", attempted, "main_flows_observed", observed, "flows_deferred", max(0, len(flows)-attempted), "next_flow_id", out.NextFlowID, "active_total", out.ActiveTotal, "collector_available", status.Available, "diagnostic_failures", len(out.DiagnosticsErrors), "duration_ms", time.Since(started).Milliseconds(), "cycle_budget_ms", 50000, "main_read_budget_ms", 5000, "collector_read_budget_ms", 3000)
	return errors.Join(failures...)
}

func (s *Service) orderedFlows(flows []twitch.Flow) []twitch.Flow {
	active := make([]twitch.Flow, 0, len(flows))
	for _, flow := range flows {
		if !flow.Archived {
			active = append(active, flow)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })
	s.flowMu.Lock()
	next := s.nextFlowID
	s.flowMu.Unlock()
	start := sort.Search(len(active), func(i int) bool { return active[i].ID >= next })
	if start == len(active) {
		start = 0
	}
	return append(active[start:], active[:start]...)
}

func prepareTasks(tasks []twitch.ActiveTask, appURL string) {
	sort.SliceStable(tasks, func(i, j int) bool {
		a, b := tasks[i], tasks[j]
		if (a.StartedAt != nil) != (b.StartedAt != nil) {
			return a.StartedAt != nil
		}
		if a.UpdatedAt != nil && b.UpdatedAt != nil && !a.UpdatedAt.Equal(*b.UpdatedAt) {
			return a.UpdatedAt.After(*b.UpdatedAt)
		}
		return a.ClipID < b.ClipID
	})
	for i := range tasks {
		if strings.HasPrefix(tasks[i].TerexURL, "/") {
			tasks[i].TerexURL = strings.TrimRight(appURL, "/") + tasks[i].TerexURL
		}
	}
}

// Enrich uses the same classifications for API diagnostics and cached panels.
func Enrich(out *domain.TwitchFlowDiagnostics, collector domain.TwitchDiagnosticCollector) {
	pending := map[string]bool{}
	for _, id := range out.PendingClipIDs {
		pending[id] = true
	}
	confirmed := map[string]bool{}
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
		case "waiting_assignment":
			state = "waiting_assignment"
		case "verification_pending":
			state = "verification_pending"
		case "authorized":
			state = "authorized"
		case "retry_wait":
			state = "retry"
		case "queued", "reserved", "waiting", "waiting_quota", "waiting_capacity", "revalidating":
			state = "queued"
		case "failed", "deferred":
			state = "failed"
		case "discovered", "filtered", "rejected":
			confirmed[item.ClipID] = true
			continue
		default:
			continue
		}
		confirmed[item.ClipID] = true
		if previous := out.PendingStates[item.ClipID]; previous != "" && out.Sources[previous] > 0 {
			out.Sources[previous]--
		}
		out.Sources[state]++
	}
	if n := out.Compilation.Sources; n != nil && len(confirmed) == len(pending) {
		n.Downloading = out.Sources["downloading"]
		n.DownloadingKnown = true
		n.DownloadPending = max(0, n.DownloadPending-n.Downloading)
	}
	out.Collector.Available = true
	out.Collector.Message = collector.Message
	out.Collector.Discovery = collector.Discovery
	if out.EstimatedAdditionalClips > out.Collector.Remaining && out.Feasibility == "needs_sources" {
		out.Feasibility = "budget_insufficient"
		out.Messages = append(out.Messages, "Оставшегося общего дневного бюджета может не хватить на подборку. Сократите минимальную длину или увеличьте бюджет; оценка не гарантирует доступность подходящих клипов")
	}
}
