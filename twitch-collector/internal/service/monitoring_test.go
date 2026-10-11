package service

import (
	"context"
	"testing"
	"time"

	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

type localMonitoringStore struct{ domain.Store }

func (localMonitoringStore) Status(context.Context) (domain.Status, error) {
	return domain.Status{Runtime: &twitch.RuntimeWork{Epoch: 42}, Queued: 3}, nil
}
func (localMonitoringStore) FlowDiagnostics(_ context.Context, _ string, settings twitch.Settings, _ time.Time) (domain.FlowDiagnostics, error) {
	return domain.FlowDiagnostics{Available: true, DailyLimit: settings.DailyDownloads}, nil
}

type forbiddenMonitoringBackend struct {
	domain.Terex
	domain.FlowTerex
	t *testing.T
}

func (f forbiddenMonitoringBackend) CollectorConfig(context.Context) (twitch.CollectorConfig, error) {
	f.t.Fatal("monitoring called main-be for config")
	return twitch.CollectorConfig{}, nil
}
func (f forbiddenMonitoringBackend) Settings(context.Context) (domain.Settings, error) {
	f.t.Fatal("monitoring called main-be for settings")
	return domain.Settings{}, nil
}
func (f forbiddenMonitoringBackend) PendingCount(context.Context) (int, error) {
	f.t.Fatal("monitoring called main-be for pending imports")
	return 0, nil
}

func TestMonitoringReadsLocalStateAndDoesNotRefreshOldConfig(t *testing.T) {
	svc := New(localMonitoringStore{}, fakeTwitch{ready: true}, forbiddenMonitoringBackend{t: t}, nil, nil)
	ctx := context.Background()
	for _, age := range []time.Duration{-1, 0, 3 * time.Minute} {
		if age >= 0 {
			at := time.Now().Add(-age).UTC()
			svc.configObservation = configObservation{observedAt: at, settings: settings()}
		}
		status, err := svc.Status(ctx)
		if err != nil || status.Runtime == nil || status.Runtime.Epoch != 42 || status.Queued != 3 {
			t.Fatalf("local runtime lost: status=%+v error=%v", status, err)
		}
		fresh := age == 0
		if status.ConfigAvailable != fresh || status.Paused == fresh {
			t.Fatalf("wrong config freshness: age=%s status=%+v", age, status)
		}
		if age >= 0 && (status.ConfigObservedAt == nil || !status.ConfigObservedAt.Equal(svc.configObservation.observedAt)) {
			t.Fatal("monitoring replaced the actual config observation time")
		}
		diagnostics, err := svc.FlowDiagnostics(ctx, "flow")
		if fresh && (err != nil || !diagnostics.Available || diagnostics.DailyLimit != settings().DailyDownloads) {
			t.Fatalf("cached config not used for local diagnostics: %+v error=%v", diagnostics, err)
		}
		if !fresh && err == nil {
			t.Fatal("diagnostics presented absent or stale config as current")
		}
	}
	// A failed refresh does not mean the still-fresh snapshot stopped work.
	svc.configObservation = configObservation{observedAt: time.Now(), settings: settings(), err: &domain.Failure{Kind: "network", Code: "terex_timeout", Message: "Terex timeout"}}
	status, err := svc.Status(ctx)
	if err != nil || status.Paused || status.BlockReason != "" || status.LastError == "" {
		t.Fatalf("fresh cached configuration falsely reported as blocked: %+v err=%v", status, err)
	}
}
