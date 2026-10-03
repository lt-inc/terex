package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	w "terex/proto/workflow"
	"testing"
	"time"
)

func twitchFixture(t *testing.T) (*Store, int64, twitch.Settings) {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, domain.Account{Platform: "youtube", Name: "Twitch test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetAccountConnection(ctx, a.ID, "connected", "UC-test", json.RawMessage(`{"bucket":"credentials","object":"test"}`), []string{}, nil, ""); err != nil {
		t.Fatal(err)
	}
	settings := twitch.DefaultSettings()
	settings.Enabled = true
	settings.TargetAccountID = a.ID
	settings.PerChannelDailyDownloads = 10
	if err = s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	return s, a.ID, settings
}
func readyImport(t *testing.T, s *Store, slug string, now time.Time) twitch.Import {
	t.Helper()
	ctx := context.Background()
	req := twitch.ImportRequest{SchemaVersion: 1, IdempotencyKey: "key:" + slug, Source: twitch.Source{Platform: "twitch", ClipID: slug, BroadcasterID: "123", BroadcasterLogin: "streamer", URL: "https://clips.twitch.tv/" + slug, Title: "Moment " + slug, CreatedAt: now.Add(-time.Hour), ObservedAt: now, ViewCount: 1000, DurationSec: 20}}
	im, err := s.ReserveTwitchImport(ctx, req, now)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(slug)))
	v := &w.VerifiedMedia{Original: w.ObjectRef{Bucket: "imports", Object: "verified/" + im.ImportID + "/" + hash + ".mp4"}, SHA256: hash, DurationSec: 20, SizeBytes: 100, Width: 1920, Height: 1080, HasAudio: true}
	im, err = s.FinishTwitchImport(ctx, im.ImportID, v, now)
	if err != nil {
		t.Fatal(err)
	}
	completeRender(t, s, im.RevisionID, now, nil)
	im, err = s.GetTwitchImport(ctx, im.ImportID)
	if err != nil {
		t.Fatal(err)
	}
	return im
}
func completeRender(t *testing.T, s *Store, rid int64, now time.Time, source *w.SourceManifest) {
	t.Helper()
	ctx := context.Background()
	var attempt string
	if err := s.pool.QueryRow(ctx, `SELECT id FROM render_attempts WHERE revision_id=$1 AND status='active'`, rid).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	revision, err := s.RevisionByID(ctx, rid)
	if err != nil {
		t.Fatal(err)
	}
	cmd := w.Command{Kind: w.KindRender, RevisionID: rid, AttemptID: attempt, ExpectedVersion: revision.Version}
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "test"}, now)
	if err != nil || !lease.Granted {
		t.Fatal(err, lease)
	}
	ok, err := s.CompleteWorkflow(ctx, &w.CompleteRequest{LeaseRequest: w.LeaseRequest{AttemptID: attempt, LeaseToken: lease.LeaseToken, ExpectedVersion: revision.Version}, Media: w.ObjectRef{Bucket: "clips", Object: fmt.Sprintf("revision-%d.mp4", rid)}, Source: source}, now)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
}
func TestTwitchImportDeduplicatesAndAtomicallyCreatesReadyWorkflow(t *testing.T) {
	s, _, _ := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	im := readyImport(t, s, "one", now)
	duplicate, err := s.ReserveTwitchImport(ctx, twitch.ImportRequest{SchemaVersion: 1, IdempotencyKey: "another-key", Source: im.Source}, now)
	if err != nil || duplicate.ImportID != im.ImportID {
		t.Fatal(duplicate, err)
	}
	if im.State != "ready" {
		t.Fatal(im.State)
	}
	job, err := s.GetJob(ctx, im.JobID)
	if err != nil || job.SourceKind != "twitch" || job.YoutubeURL != "" || job.Status != "done" {
		t.Fatal(job, err)
	}
	r, err := s.LatestRevision(ctx, im.ClipID)
	if err != nil || r.ApprovedAt != nil || r.RenderState != "ready" || r.StartSec != 0 || r.EndSec != 20 {
		t.Fatal(r, err)
	}
	if len(pubs(t, s)) != 0 {
		t.Fatal("import silently scheduled publication")
	}
}
func TestTwitchCompilationPinsSourcesReordersImmutablyAndRejectsDependency(t *testing.T) {
	s, _, _ := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	items := []twitch.CompilationItemRequest{}
	imports := []twitch.Import{}
	for _, slug := range []string{"a", "b", "c"} {
		im := readyImport(t, s, slug, now)
		imports = append(imports, im)
		items = append(items, twitch.CompilationItemRequest{ClipID: im.ClipID, RevisionID: im.RevisionID})
	}
	c, err := s.CreateCompilation(ctx, twitch.CompilationRequest{Title: "Compilation", Items: items}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.RevisionByID(ctx, c.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var manifest w.SourceManifest
	_ = json.Unmarshal(r.Manifest, &manifest)
	if manifest.Assembly == nil || manifest.Assembly.Profile != "landscape_1080p30" || manifest.DurationSec != 62 {
		t.Fatal(manifest)
	}
	manifest.Original = w.ObjectRef{Bucket: "imports", Object: "assemblies/one.mp4"}
	manifest.DurationSec = 62.03
	completeRender(t, s, c.RevisionID, now, &manifest)
	if err = s.ApproveRevision(ctx, c.ClipID, now); err != nil {
		t.Fatal(err)
	}
	items[0], items[2] = items[2], items[0]
	updated, err := s.UpdateCompilation(ctx, c.ClipID, domain.CompilationUpdate{ExpectedVersion: 1, CompilationRequest: twitch.CompilationRequest{Title: "Changed", Items: items}}, now)
	if err != nil || updated.Version != 2 || updated.State != "pending" || updated.ApprovedAt != nil {
		t.Fatal(updated, err)
	}
	if pubs(t, s)[0].State != "on_hold" {
		t.Fatal(pubs(t, s))
	}
	old, err := s.RevisionByID(ctx, c.RevisionID)
	if err != nil || old.ApprovedAt == nil || old.Object != "revision-"+fmt.Sprint(c.RevisionID)+".mp4" {
		t.Fatal(old, err)
	}
	if err = s.RejectTwitchImport(ctx, imports[0].ImportID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveRevision(ctx, c.ClipID, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("rejected dependency accepted", err)
	}
	auto, err := s.AutoCompilation(ctx, now.Add(24*time.Hour))
	if err != nil || auto != nil {
		t.Fatal(auto, err)
	}
}
func TestTwitchCapsSerializeConcurrentApprovalAndCalendarMoves(t *testing.T) {
	s, _, settings := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ims := []twitch.Import{}
	for n := 0; n < 6; n++ {
		ims = append(ims, readyImport(t, s, fmt.Sprint("clip", n), now))
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, im := range ims {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			err := s.ApproveRevision(ctx, id, now)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, domain.ErrConflict) {
				t.Errorf("approval: %v", err)
			}
		}(im.ClipID)
	}
	wg.Wait()
	if successes.Load() != int32(settings.DailyShorts) {
		t.Fatal(successes.Load())
	}
	ps := pubs(t, s)
	if len(ps) != 5 {
		t.Fatal(len(ps))
	}
	var leftover twitch.Import
	for _, im := range ims {
		r, _ := s.LatestRevision(ctx, im.ClipID)
		if r.ApprovedAt == nil {
			leftover = im
		}
	}
	if leftover.ClipID == "" {
		t.Fatal("failed approval did not roll back")
	}
	next := now.Add(24 * time.Hour)
	job, _ := s.GetJob(ctx, leftover.JobID)
	target := job.SubJobs[0].Targets[0]
	target.StartAt = &next
	if err := s.ReplaceTargets(ctx, job.SubJobs[0].SubJobID, 1, []domain.AccountTarget{target}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveRevision(ctx, leftover.ClipID, now); err != nil {
		t.Fatal(err)
	}
	ps = pubs(t, s)
	var p domain.PublicationRecord
	for _, v := range ps {
		if v.ClipID == leftover.ClipID {
			p = v
		}
	}
	if err := s.PatchPublication(ctx, p.ID, domain.PublicationPatch{Version: p.Version, PublishAt: &now}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("calendar cap bypass", err)
	}
	if err := s.ActPublication(ctx, p.ID, "publish-now", p.Version, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("publish-now cap bypass", err)
	}
}
func TestTwitchAutoCompilationCreatesAtMostOneDailyDraft(t *testing.T) {
	s, _, settings := twitchFixture(t)
	ctx := context.Background()
	loc, _ := time.LoadLocation(settings.Timezone)
	now := time.Now().In(loc)
	now = time.Date(now.Year(), now.Month(), now.Day(), 21, 0, 0, 0, loc)
	for _, slug := range []string{"one", "two", "three"} {
		readyImport(t, s, slug, now)
	}
	first, err := s.AutoCompilation(ctx, now)
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	second, err := s.AutoCompilation(ctx, now.Add(time.Minute))
	if err != nil || second != nil {
		t.Fatal(second, err)
	}
	if first.ApprovedAt != nil || first.State != "pending" || len(pubs(t, s)) != 0 {
		t.Fatal(first)
	}
}

func TestTwitchHashDuplicateAndSingleTargetCannotBypassProjectLimits(t *testing.T) {
	s, accountID, _ := twitchFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	first := readyImport(t, s, "first", now)
	req := twitch.ImportRequest{SchemaVersion: 1, IdempotencyKey: "second-key", Source: first.Source}
	req.Source.ClipID, req.Source.URL = "second", "https://clips.twitch.tv/second"
	second, err := s.ReserveTwitchImport(ctx, req, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.FinishTwitchImport(ctx, second.ImportID, &w.VerifiedMedia{Original: w.ObjectRef{Bucket: "imports", Object: "verified/duplicate.mp4"}, SHA256: first.SHA256, DurationSec: 20, SizeBytes: 100, Width: 1920, Height: 1080}, now)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("duplicate hash accepted", err)
	}
	second, _ = s.GetTwitchImport(ctx, second.ImportID)
	if !second.Rejected || second.State != "duplicate" || second.ClipID != "" {
		t.Fatal(second)
	}
	r, _ := s.LatestRevision(ctx, first.ClipID)
	if r.Title != "Moment first | streamer" || r.Description != first.Source.URL {
		t.Fatal(r.EditSpec)
	}
	other, err := s.CreateAccount(ctx, domain.Account{Platform: "youtube", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	job, _ := s.GetJob(ctx, first.JobID)
	for _, targets := range [][]domain.AccountTarget{{}, {{AccountID: other.ID, Timezone: "Asia/Tomsk"}}, {{AccountID: accountID, Timezone: "Asia/Tomsk"}, {AccountID: other.ID, Timezone: "Asia/Tomsk"}}} {
		if err = s.ReplaceTargets(ctx, job.SubJobs[0].SubJobID, 1, targets); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("invalid recipients accepted", err)
		}
	}
}

func TestTwitchGenericRevisionKeepsPinnedCompilationDependencies(t *testing.T) {
	s, _, _ := twitchFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	items := []twitch.CompilationItemRequest{}
	var first twitch.Import
	for _, slug := range []string{"a", "b", "c"} {
		im := readyImport(t, s, slug, now)
		if first.ImportID == "" {
			first = im
		}
		items = append(items, twitch.CompilationItemRequest{ClipID: im.ClipID, RevisionID: im.RevisionID})
	}
	c, err := s.CreateCompilation(ctx, twitch.CompilationRequest{Title: "Compilation", Items: items}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.RevisionByID(ctx, c.RevisionID)
	var source w.SourceManifest
	_ = json.Unmarshal(r.Manifest, &source)
	source.Original = w.ObjectRef{Bucket: "imports", Object: "assemblies/test.mp4"}
	completeRender(t, s, c.RevisionID, now, &source)
	r, _ = s.RevisionByID(ctx, c.RevisionID)
	if r.Description != "https://clips.twitch.tv/a\nhttps://clips.twitch.tv/b\nhttps://clips.twitch.tv/c" {
		t.Fatal(r.Description)
	}
	r.Title = "Metadata edit"
	next, err := s.SaveRevision(ctx, c.ClipID, domain.EditRequest{Version: 1, EditSpec: r.EditSpec}, r.Manifest, false)
	if err != nil {
		t.Fatal(err)
	}
	var deps int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM twitch_compilation_items WHERE compilation_revision_id=$1`, next.ID).Scan(&deps); err != nil || deps != 3 {
		t.Fatal(deps, err)
	}
	if err = s.RestoreRevision(ctx, c.ClipID, 2, c.RevisionID); err != nil {
		t.Fatal(err)
	}
	latest, _ := s.LatestRevision(ctx, c.ClipID)
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM twitch_compilation_items WHERE compilation_revision_id=$1`, latest.ID).Scan(&deps); err != nil || deps != 3 {
		t.Fatal(deps, err)
	}
	if err = s.RejectTwitchImport(ctx, first.ImportID, true); err != nil {
		t.Fatal(err)
	}
	if err = s.ApproveRevision(ctx, c.ClipID, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("restored compilation lost rejection guard", err)
	}
}

func TestTwitchRuntimeCapCombinesOverdueAndTodayWithoutBlockingCancellation(t *testing.T) {
	s, _, settings := twitchFixture(t)
	loc, _ := time.LoadLocation(settings.Timezone)
	today := time.Now().In(loc)
	ctx, now := context.Background(), time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, loc)
	old := readyImport(t, s, "old", now.Add(-24*time.Hour))
	if err := s.ApproveRevision(ctx, old.ClipID, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		im := readyImport(t, s, fmt.Sprint("today", n), now)
		if err := s.ApproveRevision(ctx, im.ClipID, now); err != nil {
			t.Fatal(err)
		}
	}
	// The scheduler uses an injected clock while next_attempt_at defaults to the
	// database clock. Make eligibility explicit rather than racing those clocks.
	if _, err := s.pool.Exec(ctx, `UPDATE publications SET next_attempt_at=$1`, now); err != nil {
		t.Fatal(err)
	}
	if err := s.PreparePublications(ctx, now, 100); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM publications WHERE status='queued'`).Scan(&count); err != nil || count != settings.DailyShorts {
		t.Fatal(count, err)
	}
	var waiting int64
	if err := s.pool.QueryRow(ctx, `SELECT id FROM publications WHERE status='scheduled' LIMIT 1`).Scan(&waiting); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	allowed, err := twitchRuntimeCapacity(ctx, tx, waiting, now)
	if err != nil || allowed {
		t.Fatal("overdue cap bypass", allowed, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET cancel_requested=TRUE WHERE id=$1`, waiting); err != nil {
		t.Fatal(err)
	}
	allowed, err = twitchRuntimeCapacity(ctx, tx, waiting, now)
	if err != nil || !allowed {
		t.Fatal("cap blocked cancellation", allowed, err)
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET cancel_requested=FALSE,started_at=$2 WHERE id=$1`, waiting, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	allowed, err = twitchRuntimeCapacity(ctx, tx, waiting, now)
	if err != nil || !allowed {
		t.Fatal("cap blocked already-started continuation", allowed, err)
	}
}

func TestTwitchRenderFailureAndRetryAreVisibleInJobAndImport(t *testing.T) {
	s, _, _ := twitchFixture(t)
	ctx, now := context.Background(), time.Now().UTC()
	im := readyImport(t, s, "one", now)
	prev, _ := s.LatestRevision(ctx, im.ClipID)
	next, err := s.SaveRevision(ctx, im.ClipID, domain.EditRequest{Version: 1, EditSpec: prev.EditSpec}, prev.Manifest, true)
	if err != nil {
		t.Fatal(err)
	}
	var attempt string
	if err = s.pool.QueryRow(ctx, `SELECT id FROM render_attempts WHERE revision_id=$1 AND status='active'`, next.ID).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: w.Command{Kind: w.KindRender, RevisionID: next.ID, AttemptID: attempt, ExpectedVersion: 2}, WorkerID: "test"}, now)
	if err != nil || !lease.Granted {
		t.Fatal(lease, err)
	}
	ok, err := s.FailWorkflow(ctx, &w.FailRequest{LeaseRequest: w.LeaseRequest{AttemptID: attempt, LeaseToken: lease.LeaseToken, ExpectedVersion: 2}, Message: "render failed"}, now)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	job, _ := s.GetJob(ctx, im.JobID)
	im, _ = s.GetTwitchImport(ctx, im.ImportID)
	if job.Status != "failed" || job.FailPlace != "render_revision" || im.State != "failed" || im.Error != "render failed" {
		t.Fatal(job.Status, job.FailPlace, im)
	}
	if err = s.RetryRevision(ctx, im.ClipID, 2); err != nil {
		t.Fatal(err)
	}
	job, _ = s.GetJob(ctx, im.JobID)
	im, _ = s.GetTwitchImport(ctx, im.ImportID)
	if job.Status != "processing" || job.FailPlace != "" || im.State != "rendering" || im.Error != "" {
		t.Fatal(job.Status, job.FailPlace, im)
	}
}
