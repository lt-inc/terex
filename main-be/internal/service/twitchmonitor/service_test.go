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
	work      twitch.RuntimeWork
	saved     map[string]json.RawMessage
	saveError func(string, any) error
}

func (r *snapshotRepo) TwitchRuntimeWork(context.Context, time.Time) (twitch.RuntimeWork, error) {
	return r.work, nil
}
func (r *snapshotRepo) SaveTwitchMonitoring(ctx context.Context, scope, key string, _ int64, _ time.Time, v any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.saveError != nil {
		if err := r.saveError(scope, v); err != nil {
			return err
		}
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

type monitorDiagnosticsFunc func(context.Context, string) (domain.TwitchFlowDiagnostics, error)

func (f monitorDiagnosticsFunc) Diagnostics(ctx context.Context, id string) (domain.TwitchFlowDiagnostics, error) {
	return f(ctx, id)
}

type monitorCollector struct {
	status        domain.TwitchCollectorStatus
	err           error
	flowErrors    map[string]error
	flowCalls     int
	onStatus      func()
	onDiagnostics func(context.Context, string) error
}

func (c *monitorCollector) Status(context.Context) (domain.TwitchCollectorStatus, error) {
	if c.onStatus != nil {
		c.onStatus()
	}
	return c.status, c.err
}
func (c *monitorCollector) Diagnostics(ctx context.Context, id string) (domain.TwitchDiagnosticCollector, error) {
	c.flowCalls++
	if c.onDiagnostics != nil {
		return domain.TwitchDiagnosticCollector{}, c.onDiagnostics(ctx, id)
	}
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

func TestDiagnosticBudgetPreservesProgressAndContinuesWithNextFlow(t *testing.T) {
	c := &monitorCollector{status: domain.TwitchCollectorStatus{Runtime: &twitch.RuntimeWork{Epoch: 3}}}
	s, r := monitorFixture(c)
	var attempted []string
	s.Diagnostics = monitorDiagnosticsFunc(func(ctx context.Context, id string) (domain.TwitchFlowDiagnostics, error) {
		attempted = append(attempted, id)
		if len(attempted) == 1 {
			<-ctx.Done()
			return domain.TwitchFlowDiagnostics{}, ctx.Err()
		}
		return monitorDiagnostics{}.Diagnostics(ctx, id)
	})
	r.saveError = func(scope string, v any) error {
		if scope == "system" {
			for _, failure := range v.(domain.TwitchMonitoringSnapshot).DiagnosticsErrors {
				if failure.Component == "flow_batch" {
					return errors.New("final snapshot write could not acquire a database connection")
				}
			}
		}
		return nil
	}
	// Five seconds remain reserved for writes; this leaves one second for reads.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if s.Tick(ctx) == nil {
		t.Fatal("exhausted diagnostics budget must be reported")
	}
	var out domain.TwitchMonitoringSnapshot
	if err := json.Unmarshal(r.saved["system/current"], &out); err != nil {
		t.Fatal(err)
	}
	if len(attempted) != 1 || len(out.DiagnosticsErrors) != 1 || out.DiagnosticsErrors[0].FlowID != "a" || out.DiagnosticsErrors[0].Component != "flow_main" {
		t.Fatalf("progressive failure observation was lost before failed final save: %+v; calls=%v", out, attempted)
	}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(attempted) != 3 || attempted[1] != "b" || attempted[2] != "a" {
		t.Fatalf("next cycle starved unvisited flow: %v", attempted)
	}
}

func TestCollectorTimeoutStillPersistsMainObservationAndFailure(t *testing.T) {
	c := &monitorCollector{
		status: domain.TwitchCollectorStatus{Runtime: &twitch.RuntimeWork{Epoch: 3}},
		onDiagnostics: func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	s, r := monitorFixture(c)
	// One second for diagnostics, five reserved for persisting observations.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := s.Tick(ctx); err == nil {
		t.Fatal("collector timeout must be reported")
	}
	var flow domain.TwitchFlowDiagnostics
	if err := json.Unmarshal(r.saved["flow/a"], &flow); err != nil {
		t.Fatal(err)
	}
	if flow.Sources["ready"] != 2 || flow.Collector.Available || flow.Collector.ObservedAt != nil || flow.Collector.Message != context.DeadlineExceeded.Error() {
		t.Fatalf("successful main data or collector timeout explanation lost: %+v", flow)
	}
}
