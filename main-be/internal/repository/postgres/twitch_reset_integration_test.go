package postgres

import (
	"context"
	"errors"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"testing"
	"time"
)

// One destructive-path regression covers restart/retry, uncertain publication,
// shared foreign files, late callbacks and preserved configuration together.
func TestTwitchResetPersistsDrainAndProtectsForeignReferences(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	account := flowAccount(t, s, "UC-reset")
	shared := workflow.ObjectRef{Bucket: "imports", Object: "verified/sha256/shared-reset.mp4"}
	render := workflow.ObjectRef{Bucket: "clips", Object: "twitch-reset/revision-1/result.mp4"}
	stage := workflow.ObjectRef{Bucket: "imports", Object: "staging/reset/upload.mp4"}
	for _, q := range []string{
		`INSERT INTO twitch_flows(id,config,enabled,auto_publish)VALUES('reset-flow','{"name":"keep flow"}',true,true)`,
		`INSERT INTO jobs(job_id,is_vertical,is_subtitles,source_kind)VALUES('twitch-job',false,false,'twitch'),('legacy-job',false,false,'youtube')`,
		`INSERT INTO clips(clip_id,job_id,bucket,object)VALUES('twitch-reset','twitch-job','clips','twitch-reset/revision-1/result.mp4'),('legacy-clip','legacy-job','clips','legacy-result.mp4')`,
	} {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var revision int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,manifest,bucket,object)VALUES('twitch-reset',1,'{}',$1,'clips',$2)RETURNING id`, mustJSON(workflow.SourceManifest{Original: shared}), render.Object).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,manifest)VALUES('legacy-clip',1,'{}',$1)`, mustJSON(workflow.SourceManifest{Original: shared})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE clips SET latest_revision_id=$1 WHERE clip_id='twitch-reset'`, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO render_attempts(id,revision_id,worker_id,lease_token,lease_until)VALUES('old-render',$1,'old-worker','old-token',$2)`, revision, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO publications(clip_id,account_id,platform,publish_at,status,revision_id,started_at)VALUES('twitch-reset',$1,'youtube',$2,'needs_attention',$3,$2)`, account, now, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO twitch_imports(id,idempotency_key,twitch_clip_id,request,state)VALUES('reset-import','reset-key','reset-slug','{}','discovered')`); err != nil {
		t.Fatal(err)
	}
	objects := []workflow.StorageObject{{Ref: shared, SizeBytes: 100, ModifiedAt: now}, {Ref: render, SizeBytes: 1000, ModifiedAt: now}, {Ref: stage, SizeBytes: 30, ModifiedAt: now}}
	if err := s.SyncTwitchStorage(ctx, objects, now); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewTwitchReset(ctx, now)
	if err != nil || len(preview.Blockers) != 1 {
		t.Fatalf("uncertain upload blocker: %+v %v", preview, err)
	}
	request := twitch.ResetRequest{PreviewToken: preview.PreviewToken, Confirmation: twitch.ResetConfirmation}
	if _, err = s.StartTwitchReset(ctx, request, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("uncertain upload was reset: %v", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE publications SET status='published',published_at=now()`); err != nil {
		t.Fatal(err)
	}
	run, err := s.StartTwitchReset(ctx, request, now)
	if err != nil || run.Epoch != 1 || run.State != "stopping" {
		t.Fatal(run, err)
	}
	if err = s.CheckTwitchEpoch(ctx, 0); err == nil {
		t.Fatal("late collector result admitted during reset")
	}
	if accepted, e := s.CompleteWorkflow(ctx, &workflow.CompleteRequest{LeaseRequest: workflow.LeaseRequest{AttemptID: "old-render", LeaseToken: "old-token", ExpectedVersion: 1}, Media: render}, now); e == nil && accepted {
		t.Fatal("late render result accepted")
	}
	run, err = s.AdvanceTwitchReset(ctx, run.ID, true, now)
	if err != nil || run.State != "stopping" {
		t.Fatalf("collector not drained: %+v %v", run, err)
	}
	if err = s.AcknowledgeTwitchReset(ctx, twitch.ResetCollectorAck{ID: run.ID, Epoch: 0}, now); err == nil {
		t.Fatal("wrong epoch acknowledgement accepted")
	}
	if err = s.AcknowledgeTwitchReset(ctx, twitch.ResetCollectorAck{ID: run.ID, Epoch: run.Epoch}, now); err != nil {
		t.Fatal(err)
	}
	run, err = s.AdvanceTwitchReset(ctx, run.ID, true, now)
	if err != nil || run.State != "deleting" {
		t.Fatal(run, err)
	}
	if err = s.QueueTwitchResetObjects(ctx, run.ID, now); err != nil {
		t.Fatal(err)
	}
	if allowed, e := s.CheckTwitchResetObject(ctx, run.ID, shared); e != nil || allowed {
		t.Fatalf("foreign original queued: %v %v", allowed, e)
	}
	if err = s.FinishTwitchResetObject(ctx, run.ID, render, errors.New("temporary MinIO failure"), now); err != nil {
		t.Fatal(err)
	}
	if err = s.FailTwitchReset(ctx, run.ID, "temporary MinIO failure", now); err != nil {
		t.Fatal(err)
	}
	// A new coordinator instance resumes the durable queue without clearing it.
	restarted := &Store{pool: s.pool}
	run, err = restarted.TwitchReset(ctx)
	if err != nil || run.State != "failed" {
		t.Fatalf("failed reset not persisted: %+v %v", run, err)
	}
	run, err = restarted.AdvanceTwitchReset(ctx, run.ID, true, now)
	if err != nil || run.State != "deleting" {
		t.Fatalf("failed reset did not resume: %+v %v", run, err)
	}
	if err = restarted.QueueTwitchResetObjects(ctx, run.ID, now); err != nil {
		t.Fatal(err)
	}
	pending, err := restarted.PendingTwitchResetObjects(ctx, run.ID, 50)
	if err != nil || len(pending) != 1 || pending[0].Ref != stage {
		t.Fatalf("failed deletion backoff lost: %+v %v", pending, err)
	}
	if err = restarted.ClearTwitchReset(ctx, run.ID, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("pending file metadata cleared: %v", err)
	}
	for _, ref := range []workflow.ObjectRef{render, stage} {
		if err = restarted.FinishTwitchResetObject(ctx, run.ID, ref, nil, now); err != nil {
			t.Fatal(err)
		}
	}
	if err = restarted.SyncTwitchStorage(ctx, objects[:1], now); err != nil {
		t.Fatal(err)
	}
	if err = restarted.QueueTwitchResetObjects(ctx, run.ID, now); err != nil {
		t.Fatal(err)
	}
	if err = restarted.ClearTwitchReset(ctx, run.ID, now); err != nil {
		t.Fatal(err)
	}
	run, err = restarted.TwitchReset(ctx)
	if err != nil || run.State != "clearing" {
		t.Fatalf("clearing phase not persisted: %+v %v", run, err)
	}
	if err = restarted.ClearTwitchReset(ctx, run.ID, now); err != nil {
		t.Fatal(err)
	}
	run, err = restarted.TwitchReset(ctx)
	if err != nil || run.State != "completed" || run.DeletedObjects != 2 || run.PreservedObjects != 1 {
		t.Fatal(run, err)
	}
	var imports, jobs, accounts, publications int64
	var enabled, auto bool
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*)FROM twitch_imports),(SELECT count(*)FROM jobs),(SELECT count(*)FROM accounts),(SELECT count(*)FROM publications),(SELECT enabled FROM twitch_flows WHERE id='reset-flow'),(SELECT auto_publish FROM twitch_flows WHERE id='reset-flow')`).Scan(&imports, &jobs, &accounts, &publications, &enabled, &auto); err != nil {
		t.Fatal(err)
	}
	if imports != 0 || jobs != 1 || accounts != 1 || publications != 0 || enabled || auto {
		t.Fatalf("reset scope wrong: imports=%d jobs=%d accounts=%d pubs=%d flow=%v/%v", imports, jobs, accounts, publications, enabled, auto)
	}
	if err = s.CheckTwitchEpoch(ctx, 0); err == nil {
		t.Fatal("old epoch admitted after reset")
	}
	if err = s.CheckTwitchEpoch(ctx, 1); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = storageRefsAvailable(ctx, tx, render); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("deleted object tombstone removed: %v", err)
	}
	if err = storageRefsAvailable(ctx, tx, shared); err != nil {
		t.Fatalf("foreign source made unavailable: %v", err)
	}
	_ = tx.Rollback(ctx)
	// The same public clip may be collected again after an explicit full reset,
	// but only through the new epoch and a new verified namespace.
	settings := twitch.DefaultSettings()
	settings.Enabled = true
	settings.TargetAccountID = account
	if err = s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	flow := flowFixture(t, s, account, false, now)
	source := flowSource("reset-slug", "123", 20, now)
	requestImport := twitch.ImportRequest{Epoch: 1, SchemaVersion: 2, IdempotencyKey: "reset-key", FlowID: flow.ID, FlowVersion: flow.Version, Source: source}
	imported, e := s.ReserveFlowImport(ctx, requestImport, now)
	if e != nil || imported.Epoch != 1 {
		t.Fatalf("post-reset reservation epoch: %+v %v", imported, e)
	}
	if err = s.SetFlowUpload(ctx, imported.ImportID, imported.UploadGeneration, workflow.ObjectRef{Bucket: "imports", Object: "staging/" + imported.UploadImportID + "/upload.mp4"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	media := &workflow.VerifiedMedia{Original: workflow.ObjectRef{Bucket: "imports", Object: "verified/epoch-1/sha256/reset.mp4"}, SHA256: "reset-after-epoch", DurationSec: 20, SizeBytes: 100, Width: 1920, Height: 1080}
	imported, e = s.FinishFlowImport(ctx, imported.ImportID, imported.UploadGeneration, media, now)
	if e != nil || imported.Epoch != 1 || imported.MediaState != "ready" {
		t.Fatalf("post-reset completion failed: %+v %v", imported, e)
	}
}
