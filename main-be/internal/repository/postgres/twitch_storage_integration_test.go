package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/workflow"
)

// One end-to-end invariant: sharing/restored versions and live publications
// protect physical files; expired drafts don't, and deletion survives failure.
func TestTwitchStorageSharedProtectionAndDurableDeletion(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-8 * 24 * time.Hour)
	// Required pressure data gates only preparation; unavailable/stale samples
	// never masquerade as zero usage or sufficient free space.
	for _, pressure := range []domain.TwitchStoragePressure{
		{Required: true, State: "low", ObservedAt: now, Reason: "Недостаточно места"},
		{Required: true, State: "ok", ObservedAt: now.Add(-4 * time.Minute)},
		{Required: true, State: "unknown", ObservedAt: now, Error: "Нет метрик"},
	} {
		if err := s.SaveTwitchStoragePressure(ctx, pressure); err != nil {
			t.Fatal(err)
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		err = storagePreparationAvailable(ctx, tx, now)
		_ = tx.Rollback(ctx)
		var wait *domain.PreparationWaitError
		if !errors.As(err, &wait) {
			t.Fatalf("unsafe pressure permitted preparation: %+v %v", pressure, err)
		}
	}
	if err := s.SaveTwitchStoragePressure(ctx, domain.TwitchStoragePressure{State: "off", ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	account := flowAccount(t, s, "UC-storage")
	original := workflow.ObjectRef{Bucket: "imports", Object: "verified/sha256/storage-test.mp4"}
	rendered := workflow.ObjectRef{Bucket: "clips", Object: "twitch-storage/revision-1/shared.mp4"}
	verified := mustJSON(workflow.VerifiedMedia{Original: original, SHA256: "storage-test", DurationSec: 20, SizeBytes: 100})
	manifest := mustJSON(workflow.SourceManifest{Original: original, DurationSec: 20})
	if _, err := s.pool.Exec(ctx, `INSERT INTO twitch_media(id,sha256,verified,created_at)VALUES('media','storage-test',$1,$2)`, verified, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO twitch_imports(id,idempotency_key,twitch_clip_id,request,state,media_state,media_id,verified,original,verified_at,created_at)
VALUES('source','key','slug','{}','ready','ready','media',$1,$3,$2,$2)`, verified, old, mustJSON(original)); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		flow, clip, job := fmt.Sprintf("flow-%d", n), fmt.Sprintf("twitch-storage-%d", n), fmt.Sprintf("job-%d", n)
		if _, err := s.pool.Exec(ctx, `INSERT INTO twitch_flows(id,config,account_id,youtube_channel_id,enabled)VALUES($1,'{}',$2,'UC-storage',TRUE)`, flow, account); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO jobs(job_id,is_vertical,is_subtitles,source_kind)VALUES($1,FALSE,FALSE,'twitch')`, job); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO clips(clip_id,job_id,bucket,object,approval)VALUES($1,$2,'clips',$3,'approved')`, clip, job, rendered.Object); err != nil {
			t.Fatal(err)
		}
		var rid int64
		if err := s.pool.QueryRow(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,render_state,bucket,object,manifest,created_at)
VALUES($1,1,'{}','ready','clips',$2,$3,$4) RETURNING id`, clip, rendered.Object, manifest, old).Scan(&rid); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE clips SET latest_revision_id=$2 WHERE clip_id=$1`, clip, rid); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `INSERT INTO twitch_outputs(clip_id,flow_id,source_id,account_id,youtube_channel_id,format,config,initial_revision_id)
VALUES($1,$3,'source',$4,'UC-storage','short','{}',$2)`, clip, rid, flow, account); err != nil {
			t.Fatal(err)
		}
	}
	objects := []workflow.StorageObject{{Ref: original, SizeBytes: 100, ModifiedAt: old}, {Ref: rendered, SizeBytes: 1000, ModifiedAt: old}}
	if err := s.SyncTwitchStorage(ctx, objects, now); err != nil {
		t.Fatal(err)
	}
	stats, err := s.TwitchStorage(ctx, now)
	if err != nil || stats.Objects != 2 || stats.PhysicalBytes != 1100 || stats.ReclaimableBytes != 1100 {
		t.Fatalf("unique physical inventory: %+v %v", stats, err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO twitch_imports(id,idempotency_key,twitch_clip_id,request,state,media_state,media_id,verified,original,verified_at,created_at)
VALUES('alias','alias-key','alias-slug','{}','ready','ready','media',$1,$2,$3,$3)`, verified, mustJSON(original), now.Add(-6*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncTwitchStorage(ctx, objects, now); err != nil {
		t.Fatal(err)
	}
	stats, err = s.TwitchStorage(ctx, now)
	if err != nil || stats.ReclaimableBytes != 1000 {
		t.Fatalf("new verified alias lost its seven days: %+v %v", stats, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_imports SET verified_at=$1 WHERE id='alias'`, old); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncTwitchStorage(ctx, objects, now); err != nil {
		t.Fatal(err)
	}
	if err = s.KeepTwitchStorage(ctx, "clip", "twitch-storage-2", true); err != nil {
		t.Fatal(err)
	}
	stats, err = s.TwitchStorage(ctx, now)
	if err != nil || len(stats.Items) != 2 || len(stats.Items[0].Pins) != 1 || stats.Items[0].Pins[0].ID != "twitch-storage-2" {
		t.Fatalf("exact shared pin missing: %+v %v", stats, err)
	}
	if queued, e := s.QueueTwitchCleanup(ctx, now, 50); e != nil || queued != 0 {
		t.Fatalf("shared pinned material was queued: %d %v", queued, e)
	}
	if err = s.KeepTwitchStorage(ctx, "clip", "twitch-storage-2", false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_flows SET enabled=FALSE,archived=TRUE WHERE id='flow-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO publications(clip_id,account_id,platform,publish_at,status,revision_id)
SELECT c.clip_id,$1,'youtube',$2,'scheduled',c.latest_revision_id FROM clips c WHERE clip_id='twitch-storage-2'`, account, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"scheduled", "needs_attention"} {
		if _, err = s.pool.Exec(ctx, `UPDATE publications SET status=$1`, state); err != nil {
			t.Fatal(err)
		}
		if queued, e := s.QueueTwitchCleanup(ctx, now, 50); e != nil || queued != 0 {
			t.Fatalf("shared %s publication was queued: %d %v", state, queued, e)
		}
	}
	if _, err = s.pool.Exec(ctx, `UPDATE publications SET status='published',published_at=$1`, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if queued, e := s.QueueTwitchCleanup(ctx, now, 50); e != nil || queued != 0 {
		t.Fatalf("recent successful publication lost retention: %d %v", queued, e)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE publications SET status='on_hold',published_at=NULL`); err != nil {
		t.Fatal(err)
	}
	if queued, e := s.QueueTwitchCleanup(ctx, now, 50); e != nil || queued != 2 {
		t.Fatalf("expired approved drafts held storage: %d %v", queued, e)
	}
	if err = s.KeepTwitchStorage(ctx, "clip", "twitch-storage-2", true); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("pin resurrected a deletion tombstone: %v", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = storageRefsAvailable(ctx, tx, original, rendered); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("new work accepted tombstoned media: %v", err)
	}
	_ = tx.Rollback(ctx)
	if err = s.FinishTwitchDeletion(ctx, original, errors.New("temporary storage outage"), now); err != nil {
		t.Fatal(err)
	}
	refs, err := s.PendingTwitchDeletion(ctx, now.Add(31*time.Minute), 50)
	if err != nil || len(refs) != 2 {
		t.Fatalf("durable retry missing: %+v %v", refs, err)
	}
	for _, ref := range refs {
		allowed, e := s.CheckTwitchDeletion(ctx, ref, now.Add(31*time.Minute))
		if e != nil || !allowed {
			t.Fatalf("retry unexpectedly blocked: %+v %v", ref, e)
		}
		if e = s.FinishTwitchDeletion(ctx, ref, nil, now.Add(31*time.Minute)); e != nil {
			t.Fatal(e)
		}
	}
	var imports, revisions, publications int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM twitch_imports),(SELECT count(*) FROM clip_revisions),(SELECT count(*) FROM publications)`).Scan(&imports, &revisions, &publications); err != nil || imports != 2 || revisions != 2 || publications != 1 {
		t.Fatalf("history changed: %d/%d/%d %v", imports, revisions, publications, err)
	}
	// A late PUT may recreate bytes, but must never restore availability.
	if err = s.SyncTwitchStorage(ctx, objects[:1], now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	refs, err = s.PendingTwitchDeletion(ctx, now.Add(time.Hour), 50)
	if err != nil || len(refs) != 1 || refs[0] != original {
		t.Fatalf("recreated bytes escaped cleanup: %+v %v", refs, err)
	}
}
