package postgres

import (
	"context"
	"encoding/json"
	"terex/main-be/internal/domain"
	w "terex/proto/workflow"
	"testing"
	"time"
)

func TestRenderDispatchRedeliversSameAttemptAndStopsAtDeadline(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, _, clip := fixture(t, s, nil)
	r, _ := s.LatestRevision(ctx, clip)
	raw, _ := json.Marshal(w.SourceManifest{Original: w.ObjectRef{Bucket: "downloads", Object: "source.mp4"}, DurationSec: 60})
	r, err := s.SaveRevision(ctx, clip, domain.EditRequest{Version: r.Version, EditSpec: r.EditSpec}, raw, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cmd := nextCommand(t, s, now, "render_revision")
	var outboxID string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM publication_outbox WHERE payload->>'attempt_id'=$1`, cmd.AttemptID).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkOutbox(ctx, outboxID, nil); err != nil {
		t.Fatal(err)
	}
	if err = s.PreparePublications(ctx, now.Add(16*time.Minute), 50); err != nil {
		t.Fatal(err)
	}
	repeated := nextCommand(t, s, now.Add(16*time.Minute), "render_revision")
	if repeated.AttemptID != cmd.AttemptID {
		t.Fatalf("lost delivery created another attempt: %s %s", cmd.AttemptID, repeated.AttemptID)
	}
	var attempts int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM render_attempts WHERE revision_id=$1`, r.ID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal(attempts, err)
	}
	if err = s.PreparePublications(ctx, now.Add(25*time.Hour), 50); err != nil {
		t.Fatal(err)
	}
	if err = s.PreparePublications(ctx, now.Add(26*time.Hour), 50); err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestRevision(ctx, clip)
	if err != nil || latest.RenderState != "waiting_worker" {
		t.Fatal(latest, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM render_attempts WHERE revision_id=$1`, r.ID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("deadline restarted automatically", attempts, err)
	}
	if err = s.RetryRevision(ctx, clip, r.Version); err != nil {
		t.Fatal(err)
	}
	cmd = nextCommand(t, s, time.Now().UTC(), "render_revision")
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "renderer"}, time.Now().UTC())
	if err != nil || !lease.Granted {
		t.Fatal(lease, err)
	}
	busy, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Rollback(ctx)
	if err = lockTwitch(ctx, busy); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	p, err := s.RenewWorkflow(bounded, &w.LeaseRequest{AttemptID: lease.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: lease.ExpectedVersion}, time.Now().UTC())
	if err != nil || !p.Allowed {
		t.Fatalf("renew waited behind heavy admission: %+v %v", p, err)
	}
	if err = busy.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Epoch zero is a real Twitch epoch, not a sentinel for legacy work.
	if _, err = s.pool.Exec(ctx, `UPDATE render_attempts SET twitch=true,epoch=0 WHERE id=$1`, lease.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_reset_control SET epoch=1,active=true WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	p, err = s.RenewWorkflow(ctx, &w.LeaseRequest{AttemptID: lease.AttemptID, LeaseToken: lease.LeaseToken, ExpectedVersion: lease.ExpectedVersion}, time.Now().UTC())
	if err != nil || p.Allowed {
		t.Fatalf("old epoch renewed across reset: %+v %v", p, err)
	}
}
