package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"terex/main-be/internal/domain"
	workflowservice "terex/main-be/internal/service/workflow"
	"terex/proto/twitch"
	w "terex/proto/workflow"
)

func manualRequest(t *testing.T, s *Store, clip string, account int64) domain.ManualPublishRequest {
	t.Helper()
	r, err := s.LatestRevision(context.Background(), clip)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ManualPublishRequest{RequestID: uuid.NewString(), RevisionID: r.ID, Version: r.Version, MediaKind: "rendered", AccountID: account, Platform: "youtube", Caption: "Reviewed caption", Visibility: "private", Reviewed: true}
}

func TestManualPublicationDuplicateAndRevisionFences(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	accounts, _, clip := fixture(t, s, nil)
	u := manualRequest(t, s, clip, accounts[0].ID)
	if _, e := s.pool.Exec(ctx, `UPDATE clips SET latest_revision_id=NULL WHERE clip_id=$1`, clip); e != nil {
		t.Fatal(e)
	}
	options, e := (&workflowservice.Service{Repo: s}).ClipPublicationOptions(ctx, clip)
	if e != nil || options.RevisionID != u.RevisionID || len(options.Variants[0].Platforms) != 1 || options.Variants[0].Platforms[0] != "youtube" {
		t.Fatal("legacy eligibility", options, e)
	}
	var wg sync.WaitGroup
	results := make(chan domain.PublicationRecord, 6)
	failures := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := s.CreateManualPublication(ctx, clip, u, now)
			results <- p
			failures <- e
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	var p domain.PublicationRecord
	for result := range results {
		if p.ID != 0 && p.ID != result.ID {
			t.Fatal("duplicate publication", p, result)
		}
		p = result
	}
	if !p.Manual || p.RevisionID == u.RevisionID || p.Visibility != "private" {
		t.Fatal("manual snapshot missing", p)
	}
	// Database defaults on the outbox use the real clock; concurrent inserts
	// can take longer than one second on a busy builder.
	now = time.Now().UTC()
	cmd := nextCommand(t, s, now.Add(time.Second), "publish")
	r, _ := s.LatestRevision(ctx, clip)
	if r.ApprovedAt != nil {
		t.Fatal("manual click approved editor/flow revision")
	}
	r.Title = "Later edit"
	if _, e := s.SaveRevision(ctx, clip, domain.EditRequest{Version: r.Version, EditSpec: r.EditSpec}, nil, false); e != nil {
		t.Fatal(e)
	}
	if e := s.ApproveRevisionWithVersion(ctx, clip, r.Version+1, now); e != nil {
		t.Fatal(e)
	}
	after, e := s.GetPublicationRecord(ctx, p.ID)
	if e != nil || after.RevisionID != p.RevisionID || after.Caption != p.Caption || after.Status != "queued" {
		t.Fatal("editor retargeted manual publication", after, e)
	}
	denied, e := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: w.Command{Kind: w.KindPublish, PublicationID: p.ID, AttemptID: cmd.AttemptID, ExpectedVersion: cmd.ExpectedVersion + 1}, WorkerID: "stale"}, now.Add(time.Second))
	if e != nil || denied.Granted {
		t.Fatal("stale version claimed", denied, e)
	}
	lease, e := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "manual"}, now.Add(time.Second))
	if e != nil || !lease.Granted || lease.Publication.RevisionID != p.RevisionID {
		t.Fatal("manual dispatch failed", lease, e)
	}
	// A lost final response stays attached to this publication across both the
	// original request key and a new UUID; neither creates an external retry.
	if _, e = s.pool.Exec(ctx, `UPDATE publications SET status='needs_attention',error_code='upload_unknown' WHERE id=$1`, p.ID); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		replay, e := s.CreateManualPublication(ctx, clip, u, now)
		if e != nil || replay.ID != p.ID || replay.Status != "needs_attention" {
			t.Fatal("uncertain replay", replay, e)
		}
		u.RequestID = uuid.NewString()
	}
	u.Caption = "Different request"
	if _, e = s.CreateManualPublication(ctx, clip, u, now); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("fresh UUID bypassed existing outcome", e)
	}
	alias := flowAccount(t, s, "UC-test")
	u.AccountID = alias
	if _, e = s.CreateManualPublication(ctx, clip, u, now); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("OAuth alias bypassed existing outcome", e)
	}
}

func TestManualPublicationReadiness(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	accounts, _, clip := fixture(t, s, nil)
	u := manualRequest(t, s, clip, accounts[0].ID)
	u.Version++
	if _, e := s.CreateManualPublication(ctx, clip, u, now); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale revision accepted", e)
	}
	u.Version--
	u.Platform = "tiktok"
	if _, e := s.CreateManualPublication(ctx, clip, u, now); !errors.Is(e, domain.ErrInvalid) {
		t.Fatal("platform mismatch accepted", e)
	}
	u.Platform = "youtube"
	if _, e := s.pool.Exec(ctx, `UPDATE accounts SET connection_state='disconnected' WHERE id=$1`, u.AccountID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateManualPublication(ctx, clip, u, now); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("disconnected account accepted", e)
	}
	if _, e := s.pool.Exec(ctx, `UPDATE accounts SET platform='tiktok',connection_state='connected',credential_ref='{"bucket":"tiktok-sessions","object":"test.json"}' WHERE id=$1`, u.AccountID); e != nil {
		t.Fatal(e)
	}
	u.Platform = "tiktok"
	if _, e := s.CreateManualPublication(ctx, clip, u, now); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("missing TikTok session accepted", e)
	}
	if _, e := s.pool.Exec(ctx, `UPDATE accounts SET session_version=1 WHERE id=$1`, u.AccountID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateManualPublication(ctx, clip, u, now); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("missing TikTok media SHA accepted", e)
	}
}

func TestManualCompilationRebuildKeepsQueuedSnapshot(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	items := []twitch.CompilationItemRequest{}
	for _, slug := range []string{"manual-a", "manual-b", "manual-c"} {
		im := readyImport(t, s, slug, now)
		items = append(items, twitch.CompilationItemRequest{ClipID: im.ClipID, RevisionID: im.RevisionID})
	}
	c, err := s.CreateCompilation(ctx, twitch.CompilationRequest{Title: "Reviewed compilation", Items: items}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	completeRender(t, s, c.RevisionID, now, nil)
	p, err := s.CreateManualPublication(ctx, c.ClipID, manualRequest(t, s, c.ClipID, account), now)
	if err != nil {
		t.Fatal(err)
	}
	now = time.Now().UTC().Add(time.Second)
	cmd := nextCommand(t, s, now, "publish")
	before, err := s.GetPublicationRecord(ctx, p.ID)
	if err != nil || before.Status != "queued" || before.StartedAt != nil {
		t.Fatal("expected queued unstarted manual target", before, err)
	}
	snapshot, err := s.RevisionByID(ctx, p.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	items[0], items[2] = items[2], items[0]
	updated, err := s.UpdateCompilation(ctx, c.ClipID, domain.CompilationUpdate{ExpectedVersion: c.Version, CompilationRequest: twitch.CompilationRequest{Title: "Rebuilt compilation", Items: items}}, now)
	if err != nil || updated.State != "pending" || updated.RevisionID == c.RevisionID {
		t.Fatal("compilation did not rebuild", updated, err)
	}
	after, err := s.GetPublicationRecord(ctx, p.ID)
	if err != nil || after.RevisionID != before.RevisionID || after.Version != before.Version || after.Status != before.Status || !reflect.DeepEqual(after.PublicationMetadata, before.PublicationMetadata) {
		t.Fatal("rebuild changed manual target", before, after, err)
	}
	var attemptStatus string
	if err = s.pool.QueryRow(ctx, `SELECT status FROM publication_attempts WHERE id=$1`, cmd.AttemptID).Scan(&attemptStatus); err != nil || attemptStatus != "active" {
		t.Fatal("rebuild superseded manual attempt", attemptStatus, err)
	}
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "manual-compilation"}, now)
	if err != nil || !lease.Granted || lease.Publication.RevisionID != snapshot.ID || lease.Publication.Media != (w.ObjectRef{Bucket: snapshot.Bucket, Object: snapshot.Object}) {
		t.Fatal("queued snapshot no longer dispatches", lease, err)
	}
}

func TestManualPublicationBlocksAliasScheduling(t *testing.T) {
	t.Run("approval", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		now := time.Now().UTC()
		accounts, _, clip := fixture(t, s, make([]domain.AccountTarget, 3))
		if _, err := s.pool.Exec(ctx, `UPDATE accounts SET remote_account_id='UC-other' WHERE id=$1`, accounts[1].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE accounts SET platform='tiktok' WHERE id=$1`, accounts[2].ID); err != nil {
			t.Fatal(err)
		}
		alias := flowAccount(t, s, "UC-test")
		p, err := s.CreateManualPublication(ctx, clip, manualRequest(t, s, clip, alias), now)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ApproveRevision(ctx, clip, now); err != nil {
			t.Fatal(err)
		}
		ps := pubs(t, s)
		if len(ps) != 3 {
			t.Fatal("approval duplicated manual destination or suppressed another destination", ps)
		}
		for _, publication := range ps {
			if publication.ID != p.ID && (publication.AccountID == accounts[0].ID || publication.Status != "scheduled") {
				t.Fatal("approval scheduled alias", publication)
			}
		}
	})
	t.Run("flow", func(t *testing.T) {
		s, account, _ := twitchFixture(t)
		ctx := context.Background()
		now := time.Now().UTC()
		f := flowFixture(t, s, account, true, now)
		v := finishFlow(t, s, reserveFlow(t, s, f, flowSource("manual-before-scheduler", "123", 20, now), now), 20, "", now)
		completeRender(t, s, v.RevisionID, now, nil)
		alias := flowAccount(t, s, "UC-test")
		p, err := s.CreateManualPublication(ctx, v.ClipID, manualRequest(t, s, v.ClipID, alias), now)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.scheduleFlowOutputs(ctx, now); err != nil {
			t.Fatal(err)
		}
		ps := pubs(t, s)
		if len(ps) != 1 || ps[0].ID != p.ID {
			t.Fatal("scheduler duplicated manual destination", ps)
		}
	})
}

func TestManualPublicationYouTubeMetadataValidation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	accounts, _, clip := fixture(t, s, nil)
	u := manualRequest(t, s, clip, accounts[0].ID)
	for _, tc := range []struct{ title, caption string }{
		{strings.Repeat("я", 101), "Caption"},
		{"Invalid <title>", "Caption"},
		{"Valid title", "Invalid <caption>"},
	} {
		if _, err := s.pool.Exec(ctx, `UPDATE clip_revisions SET spec=jsonb_set(spec,'{title}',to_jsonb($2::text)) WHERE id=$1`, u.RevisionID, tc.title); err != nil {
			t.Fatal(err)
		}
		u.Caption = tc.caption
		if _, err := s.CreateManualPublication(ctx, clip, u, time.Now().UTC()); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("invalid YouTube metadata accepted", tc, err)
		}
	}
	var snapshots, attempts, outbox int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM clip_revisions WHERE publication_only),(SELECT count(*) FROM publication_attempts),(SELECT count(*) FROM publication_outbox)`).Scan(&snapshots, &attempts, &outbox); err != nil || snapshots != 0 || attempts != 0 || outbox != 0 || len(pubs(t, s)) != 0 {
		t.Fatal("invalid metadata left publication work", snapshots, attempts, outbox, err)
	}
}

func TestManualVerifiedSourceWithoutActiveFlow(t *testing.T) {
	s, owner, _ := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	f := flowFixture(t, s, owner, false, now)
	v := finishFlow(t, s, reserveFlow(t, s, f, flowSource("manual-source", "123", 20, now), now), 20, "", now)
	if _, e := s.pool.Exec(ctx, `UPDATE twitch_flows SET enabled=FALSE,archived=TRUE WHERE id=$1`, f.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.pool.Exec(ctx, `UPDATE twitch_policy_settings SET mode='enforce' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	now = now.Add(96 * time.Hour)
	a, e := s.CreateAccount(ctx, domain.Account{Platform: "tiktok", Name: "manual TikTok"})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetAccountConnection(ctx, a.ID, "connected", "12345", []byte(`{"bucket":"tiktok-sessions","object":"test.json"}`), nil, nil, ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE accounts SET session_version=1 WHERE id=$1`, a.ID); e != nil {
		t.Fatal(e)
	}
	u := manualRequest(t, s, v.ClipID, a.ID)
	u.Platform = "tiktok"
	u.MediaKind = "source"
	options, e := s.ClipPublicationOptions(ctx, v.ClipID)
	if e != nil || !options.Variants[1].Eligible || options.Variants[0].Eligible {
		t.Fatal("source eligibility", options, e)
	}
	if _, e := s.CreateManualPublication(ctx, v.ClipID, u, now); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale source accepted without scoped consent", e)
	}
	u.FreshnessOverride = true
	if _, e := s.pool.Exec(ctx, `INSERT INTO twitch_channel_limits(youtube_channel_id,timezone,daily_shorts,daily_compilations)VALUES('tiktok:12345','UTC',0,1)`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateManualPublication(ctx, v.ClipID, u, now); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("manual publication bypassed quota", e)
	}
	if _, e := s.pool.Exec(ctx, `UPDATE twitch_channel_limits SET daily_shorts=3 WHERE youtube_channel_id='tiktok:12345'`); e != nil {
		t.Fatal(e)
	}
	p, e := s.CreateManualPublication(ctx, v.ClipID, u, now)
	if e != nil {
		t.Fatal(e)
	}
	if !p.Manual || p.RevisionID == u.RevisionID {
		t.Fatal("missing detached source snapshot", p)
	}
	cmd := nextCommand(t, s, now.Add(time.Second), "publish_tiktok")
	lease, e := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "tiktok"}, now.Add(time.Second))
	if e != nil || !lease.Granted || lease.Publication.Media != v.Original || lease.Publication.MediaSHA256 != v.SHA256 || lease.Publication.Visibility != "private" {
		t.Fatal("source dispatch", lease, e)
	}
	r, _ := s.LatestRevision(ctx, v.ClipID)
	if r.ID != u.RevisionID || r.RenderState == "ready" || r.ApprovedAt != nil {
		t.Fatal("source publication changed original pipeline", r)
	}
	if _, e = s.pool.Exec(ctx, `UPDATE clip_revisions SET storage_state='expired' WHERE id=$1`, r.ID); e != nil {
		t.Fatal(e)
	}
	u.AccountID = flowAccount(t, s, "UC-other")
	u.Platform = "youtube"
	u.RequestID = uuid.NewString()
	if _, e = s.CreateManualPublication(ctx, v.ClipID, u, now); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("expired source accepted", e)
	}
}
