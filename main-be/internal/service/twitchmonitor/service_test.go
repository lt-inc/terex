package twitchmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/twitch"
)

type snapshotRepo struct {
	work  twitch.RuntimeWork
	saved map[string]json.RawMessage
}

func (r *snapshotRepo) TwitchRuntimeWork(context.Context, time.Time) (twitch.RuntimeWork, error) {
	return r.work, nil
}
func (r *snapshotRepo) SaveTwitchMonitoring(ctx context.Context, scope, key string, _ int64, _ time.Time, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	r.saved[scope+"/"+key] = raw
	return err
}
func (r *snapshotRepo) CleanupTwitchMonitoring(context.Context, int64) error { return nil }

type monitorFlows struct {
	domain.TwitchFlowRepo
	items []twitch.Flow
}

func (r monitorFlows) ListTwitchFlows(context.Context) ([]twitch.Flow, error) { return r.items, nil }

type monitorDiagnostics struct{}

func (monitorDiagnostics) Diagnostics(_ context.Context, id string) (domain.TwitchFlowDiagnostics, error) {
	return domain.TwitchFlowDiagnostics{FlowID: id, Sources: map[string]int{"ready": 2}}, nil
}

type monitorCollector struct {
	status     domain.TwitchCollectorStatus
	err        error
	flowErrors map[string]error
	flowCalls  int
	onStatus   func()
}

func (c *monitorCollector) Status(context.Context) (domain.TwitchCollectorStatus, error) {
	if c.onStatus != nil {
		c.onStatus()
	}
	return c.status, c.err
}
func (c *monitorCollector) Diagnostics(_ context.Context, id string) (domain.TwitchDiagnosticCollector, error) {
	c.flowCalls++
	return domain.TwitchDiagnosticCollector{Discovery: []domain.TwitchDiagnosticDiscovery{{FlowID: id, Fetched: 10}}}, c.flowErrors[id]
}

func monitorFixture(c *monitorCollector) (*Service, *snapshotRepo) {
	r := &snapshotRepo{
		work:  twitch.RuntimeWork{Epoch: 3, Counts: []twitch.StageCount{{Stage: "render", Count: 2}}, ActiveTasks: []twitch.ActiveTask{{ClipID: "short", Stage: "render"}}, ActiveTotal: 2},
		saved: map[string]json.RawMessage{},
	}
	s := &Service{Repo: r, Flows: monitorFlows{items: []twitch.Flow{{ID: "a", Name: "First"}, {ID: "b", Name: "Second"}}}, Diagnostics: monitorDiagnostics{}, Collector: c}
	return s, r
}

func TestCollectorFailureDoesNotHideMainSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status domain.TwitchCollectorStatus
		err    error
	}{
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "old epoch", status: domain.TwitchCollectorStatus{Runtime: &twitch.RuntimeWork{Epoch: 2, Counts: []twitch.StageCount{{Stage: "download", Count: 9}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &monitorCollector{status: tc.status, err: tc.err}
			s, r := monitorFixture(c)
			c.onStatus = func() {
				if len(r.saved["system/current"]) == 0 {
					t.Fatal("main runtime must be saved before collector reads")
				}
			}
			if s.Tick(context.Background()) == nil {
				t.Fatal("unavailable collector must be reported")
			}
			var out domain.TwitchMonitoringSnapshot
			if err := json.Unmarshal(r.saved["system/current"], &out); err != nil {
				t.Fatal(err)
			}
			if out.CollectorStatus.Available || out.CollectorObservedAt != nil || out.CollectorStatus.LastError == "" {
				t.Fatalf("missing collector was marked fresh: %+v", out.CollectorStatus)
			}
			if out.ActiveTotal != 2 || len(out.Counts) != 1 || out.Counts[0].Stage != "render" || len(out.DiagnosticsErrors) != 1 {
				t.Fatalf("main work lost or unavailable collector counted: %+v", out)
			}
			for _, id := range []string{"a", "b"} {
				var flow domain.TwitchFlowDiagnostics
				if err := json.Unmarshal(r.saved["flow/"+id], &flow); err != nil {
					t.Fatal(err)
				}
				if flow.Sources["ready"] != 2 || flow.Collector.Available || flow.Collector.Message == "" {
					t.Fatalf("main flow observations lost: %+v", flow)
				}
			}
			if c.flowCalls != 0 {
				t.Fatal("collector outage must not repeat its timeout for every flow")
			}
		})
	}
}

func TestFlowCollectorFailureDoesNotHideOtherFlows(t *testing.T) {
	c := &monitorCollector{
		status:     domain.TwitchCollectorStatus{Runtime: &twitch.RuntimeWork{Epoch: 3, Counts: []twitch.StageCount{{Stage: "download", Count: 7}}, ActiveTotal: 7}},
		flowErrors: map[string]error{"a": errors.New("collector flow read failed")},
	}
	s, r := monitorFixture(c)
	if s.Tick(context.Background()) == nil {
		t.Fatal("partial diagnostics failure must be reported")
	}
	var out domain.TwitchMonitoringSnapshot
	if err := json.Unmarshal(r.saved["system/current"], &out); err != nil {
		t.Fatal(err)
	}
	if !out.CollectorStatus.Available || out.CollectorObservedAt == nil || out.ActiveTotal != 9 || len(out.Discovery) != 1 || len(out.DiagnosticsErrors) != 1 {
		t.Fatalf("successful runtime/discovery was hidden: %+v", out)
	}
	for _, id := range []string{"a", "b"} {
		var flow domain.TwitchFlowDiagnostics
		if err := json.Unmarshal(r.saved["flow/"+id], &flow); err != nil {
			t.Fatal(err)
		}
		if flow.Sources["ready"] != 2 || flow.Collector.Available != (id == "b") || (flow.Collector.ObservedAt != nil) != (id == "b") {
			t.Fatalf("wrong component freshness for %s: %+v", id, flow)
		}
	}
}
