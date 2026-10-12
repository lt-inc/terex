package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/pgxutil"
	"terex/proto/twitch"
	w "terex/proto/workflow"
)

func TestControlPoolPreservesConnectionBudget(t *testing.T) {
	cfg, err := pgxutil.ParseConfig("postgres://postgres@localhost/test?pool_max_conns=4&pool_min_conns=4&pool_min_idle_conns=4")
	if err != nil {
		t.Fatal(err)
	}
	work, control := splitControlPool(cfg, 1)
	if work.MaxConns != 3 || control.MaxConns != 1 || work.MinConns+control.MinConns != cfg.MinConns || work.MinIdleConns+control.MinIdleConns != cfg.MinIdleConns {
		t.Fatal("connection budget changed", work, control)
	}
	for _, maxConns := range []int32{1, 4} {
		cfg.MaxConns, cfg.MinConns, cfg.MinIdleConns = maxConns, 0, 0
		reserved := int32(1)
		if maxConns == 4 {
			reserved = 0
		}
		work, control = splitControlPool(cfg, reserved)
		if control != nil || work.MaxConns != maxConns {
			t.Fatal("disabled isolation added connections")
		}
	}
}

func TestControlReadsProgressWithOccupiedWorkPool(t *testing.T) {
	t.Setenv("DB_CONTROL_POOL_RESERVED_CONNS", "1")
	s, account, _ := twitchFixture(t)
	flowFixture(t, s, account, false, time.Now().UTC())
	ctx := context.Background()
	_, _, clip := fixture(t, s, nil)
	revision, err := s.LatestRevision(ctx, clip)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(w.SourceManifest{Original: w.ObjectRef{Bucket: "downloads", Object: "source.mp4"}, DurationSec: 60})
	if _, err = s.SaveRevision(ctx, clip, domain.EditRequest{Version: revision.Version, EditSpec: revision.EditSpec}, raw, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	command := nextCommand(t, s, now, "render_revision")
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: command, WorkerID: "control-test"}, now)
	if err != nil || !lease.Granted {
		t.Fatal(lease, err)
	}
	for range s.pool.Config().MaxConns {
		conn, err := s.pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	cfg, err := s.TwitchCollectorConfig(bounded)
	if err != nil || len(cfg.Flows) == 0 {
		t.Fatal("config waited behind occupied work connections", cfg, err)
	}
	if _, err = s.TwitchCollectorPressure(bounded, twitch.PressureRequest{Epoch: cfg.Epoch}, time.Now().UTC()); err != nil {
		t.Fatal("pressure waited behind occupied work connections", err)
	}
	if _, err = s.TwitchResetControl(bounded); err != nil {
		t.Fatal("maintenance sync waited behind occupied work connections", err)
	}
	permit, err := s.RenewWorkflow(bounded, &w.LeaseRequest{AttemptID: command.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: command.ExpectedVersion}, now)
	if err != nil || !permit.Allowed {
		t.Fatal("render renewal waited behind occupied work connections", permit, err)
	}
	permit, err = s.RenewWorkflow(bounded, &w.LeaseRequest{AttemptID: command.AttemptID, LeaseToken: "wrong-token", ExpectedVersion: command.ExpectedVersion}, now)
	if err != nil || permit.Allowed {
		t.Fatal("control renewal bypassed lease checks", permit, err)
	}
}
