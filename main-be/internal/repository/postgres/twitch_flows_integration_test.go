package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	w "terex/proto/workflow"
	"testing"
	"time"
)

func flowAccount(t *testing.T, s *Store, remote string) int64 {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, domain.Account{Platform: "youtube", Name: remote})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetAccountConnection(ctx, a.ID, "connected", remote, json.RawMessage(`{"bucket":"oauth","object":"test"}`), nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	return a.ID
}
func flowFixture(t *testing.T, s *Store, account int64, auto bool, now time.Time) twitch.Flow {
	t.Helper()
	f := defaultFlow()
	f.AccountID = account
	f.Enabled = true
	f.AutoPublish = auto
	f.BroadcasterIDs = []string{"123", "456"}
	f.Timezone = "UTC"
	f.CompilationHour = 0
	out, err := s.SaveTwitchFlow(context.Background(), f, true, now)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func flowSource(slug, broadcaster string, duration float64, now time.Time) twitch.Source {
	return twitch.Source{Platform: "twitch", ClipID: slug, BroadcasterID: broadcaster, BroadcasterLogin: "streamer", URL: "https://clips.twitch.tv/" + slug, Title: slug, CreatedAt: now.Add(-2 * time.Hour), ObservedAt: now, ViewCount: 1000, DurationSec: duration, Language: "ru"}
}
func reserveFlow(t *testing.T, s *Store, f twitch.Flow, src twitch.Source, now time.Time) twitch.Import {
	t.Helper()
	v, err := s.ReserveFlowImport(context.Background(), twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "test:" + src.ClipID, FlowID: f.ID, FlowVersion: f.Version, Source: src}, now)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func finishFlow(t *testing.T, s *Store, v twitch.Import, duration float64, hash string, now time.Time) twitch.Import {
	t.Helper()
	if hash == "" {
		hash = fmt.Sprintf("%x", sha256.Sum256([]byte(v.Source.ClipID)))
	}
	media := &w.VerifiedMedia{Original: w.ObjectRef{Bucket: "imports", Object: "verified/" + v.ImportID + "/" + hash + ".mp4"}, SHA256: hash, DurationSec: duration, SizeBytes: 100, Width: 1920, Height: 1080, HasAudio: true}
	_, err := s.FinishFlowImport(context.Background(), v.ImportID, v.UploadGeneration, media, now)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.GetFlowImport(context.Background(), v.ImportID, v.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTwitchFlowsShareVerifiedMediaAndFencePinnedUpload(t *testing.T) {
	s, account, settings := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	settings.DailyDownloads = 3
	settings.PerChannelDailyDownloads = 3
	if err := s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	a := flowFixture(t, s, account, false, now)
	b := flowFixture(t, s, flowAccount(t, s, "UC-other"), false, now)
	alias := flowFixture(t, s, flowAccount(t, s, "UC-test"), false, now)
	src := flowSource("shared", "123", 20, now)
	metadata := []twitch.Source{src}
	for n := 0; n < 41; n++ {
		metadata = append(metadata, flowSource(fmt.Sprintf("metadata-%d", n), "123", 20, now))
	}
	if _, err := s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: b.ID, FlowVersion: b.Version, Sources: metadata}, now); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.ListTwitchImports(ctx, true, ""); err != nil || len(pending) != 0 {
		t.Fatal("metadata blocked download queue", pending, err)
	}
	initial := reserveFlow(t, s, a, src, now)
	if _, err := s.ReserveFlowImport(ctx, twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "test:" + src.ClipID, FlowID: a.ID, FlowVersion: a.Version, Source: flowSource("wrong-key", "123", 20, now)}, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("idempotency key rebound", err)
	}
	if _, err := s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: b.ID, FlowVersion: b.Version, Sources: []twitch.Source{src}}, now); err != nil {
		t.Fatal(err)
	}
	a.Enabled = false
	var err error
	a, err = s.SaveTwitchFlow(ctx, a, false, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ActFlowSource(ctx, a.ID, initial.ImportID, "exclude", true, now); err != nil {
		t.Fatal(err)
	}
	retryRequest := twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "test:" + src.ClipID, Source: src, FlowID: a.ID, FlowVersion: initialFlowVersion(t, s, a.ID, initial.ImportID), ReservationID: initial.ReservationID, UploadGeneration: initial.UploadGeneration}
	resumed, err := s.ReserveFlowImport(ctx, retryRequest, now.Add(2*time.Second))
	if err != nil || resumed.UploadGeneration != initial.UploadGeneration || resumed.Rejected || !resumed.Excluded {
		t.Fatal(resumed, err)
	}
	ready := finishFlow(t, s, resumed, 20, "", now.Add(3*time.Second))
	if ready.MediaState != "ready" || ready.ClipID != "" {
		t.Fatal(ready)
	}
	other, err := s.GetFlowImport(ctx, ready.ImportID, b.ID)
	if err != nil || other.ClipID == "" {
		t.Fatal(other, err)
	}
	a.Enabled = true
	a, err = s.SaveTwitchFlow(ctx, a, false, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ActFlowSource(ctx, a.ID, ready.ImportID, "exclude", false, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	own, err := s.GetFlowImport(ctx, ready.ImportID, a.ID)
	if err != nil || own.ClipID == "" || own.ClipID == other.ClipID {
		t.Fatal(own, other, err)
	}
	observed, err := s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: alias.ID, FlowVersion: alias.Version, Sources: []twitch.Source{src}}, now)
	if err != nil || len(observed) != 1 || observed[0].MediaID != own.MediaID || observed[0].ClipID != "" {
		t.Fatal(observed, err)
	}
	hash := own.SHA256
	copySrc := flowSource("same-bytes", "456", 20, now)
	copyImport := finishFlow(t, s, reserveFlow(t, s, b, copySrc, now), 20, hash, now)
	if copyImport.MediaID != own.MediaID || copyImport.ClipID != "" || copyImport.Original != own.Original {
		t.Fatal(copyImport, own)
	}
	var mediaCount int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM twitch_media`).Scan(&mediaCount); err != nil || mediaCount != 1 {
		t.Fatal(mediaCount, err)
	}
	fresh := reserveFlow(t, s, a, flowSource("retry-fence", "123", 20, now), now)
	if _, err = s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: b.ID, FlowVersion: b.Version, Sources: []twitch.Source{fresh.Source}}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.ActFlowSource(ctx, a.ID, fresh.ImportID, "retry", false, now); err != nil {
		t.Fatal(err)
	}
	_, err = s.FinishFlowImport(ctx, fresh.ImportID, fresh.UploadGeneration, &w.VerifiedMedia{SHA256: hash, Original: own.Original, DurationSec: 20}, now)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("late completion accepted after retry fence", err)
	}
	if err = s.SetFlowUpload(ctx, fresh.ImportID, fresh.UploadGeneration, own.Original, now.Add(time.Minute)); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("late upload accepted after retry fence", err)
	}
	if err = s.DeleteTwitchFlow(ctx, a.ID, now); err != nil {
		t.Fatal(err)
	}
	request := twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "test:" + fresh.Source.ClipID, Source: fresh.Source, FlowID: a.ID, FlowVersion: a.Version, ReservationID: fresh.ReservationID, UploadGeneration: fresh.UploadGeneration}
	next, err := s.ReserveFlowImport(ctx, request, now.Add(time.Second))
	if err != nil || next.UploadGeneration <= fresh.UploadGeneration {
		t.Fatal(next, err)
	}
	lostResponse, err := s.ReserveFlowImport(ctx, request, now.Add(2*time.Second))
	if err != nil || lostResponse.UploadGeneration != next.UploadGeneration {
		t.Fatal("lost renewal response was not idempotent", lostResponse, err)
	}
	// The collector may still hold gen1 when an operator retries after the
	// gen2 response was lost. Reset fences gen2, and saved gen1 issues gen3.
	if err = s.ActFlowSource(ctx, b.ID, fresh.ImportID, "retry", false, now, next.UploadGeneration); err != nil {
		t.Fatal(err)
	}
	_, err = s.FinishFlowImport(ctx, fresh.ImportID, next.UploadGeneration, &w.VerifiedMedia{SHA256: hash, Original: own.Original, DurationSec: 20}, now)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("pre-reset renewed generation completed", err)
	}
	recovered, err := s.ReserveFlowImport(ctx, request, now.Add(3*time.Second))
	if err != nil || recovered.UploadGeneration <= next.UploadGeneration || recovered.State != "upload_pending" {
		t.Fatal("lost response plus operator retry did not recover", recovered, err)
	}
	repeated, err := s.ReserveFlowImport(ctx, request, now.Add(4*time.Second))
	if err != nil || repeated.UploadGeneration != recovered.UploadGeneration {
		t.Fatal("lost recovery response was not idempotent", repeated, err)
	}
	_, err = s.FinishFlowImport(ctx, fresh.ImportID, fresh.UploadGeneration, &w.VerifiedMedia{SHA256: hash, Original: own.Original, DurationSec: 20}, now)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale upload accepted", err)
	}
	if _, err = s.ReserveFlowImport(ctx, twitch.ImportRequest{SchemaVersion: 2, IdempotencyKey: "test:budget-full", FlowID: b.ID, FlowVersion: b.Version, Source: flowSource("budget-full", "123", 20, now)}, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("shared daily budget bypassed", err)
	}
	if err = s.RejectTwitchImport(ctx, own.ImportID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TwitchSourcePreview(ctx, own.ImportID, b.ID); err != nil {
		t.Fatal("verified file lost after rejection", err)
	}
	page, err := s.ListFlowClips(ctx, b.ID, domain.FlowClipQuery{Limit: 50})
	if err != nil || page.Total != 44 {
		t.Fatal(page, err)
	}
}
func initialFlowVersion(t *testing.T, s *Store, flow, id string) int {
	t.Helper()
	var v int
	if err := s.pool.QueryRow(context.Background(), `SELECT flow_version FROM twitch_flow_sources WHERE flow_id=$1 AND import_id=$2`, flow, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTwitchFlowsScheduleAtomicallyAndKeepEditedCompilationsManual(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx := context.Background()
	day := time.Now().UTC()
	now := time.Date(day.Year(), day.Month(), day.Day(), 8, 0, 0, 0, time.UTC)
	f := flowFixture(t, s, account, true, now)
	f.DailyShorts = 2
	f.ShortsSlots = []string{"09:00", "12:00"}
	f.CompilationMinDurationSec = 81
	f.CompilationMaxDurationSec = 84
	var err error
	f, err = s.SaveTwitchFlow(ctx, f, false, now)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 4; n++ {
		source := flowSource(fmt.Sprintf("balanced-%d", n), []string{"123", "456"}[n%2], 20.001, now)
		v := finishFlow(t, s, reserveFlow(t, s, f, source, now.Add(time.Duration(n)*time.Second)), 20.001, "", now.Add(time.Duration(n)*time.Second))
		completeRender(t, s, v.RevisionID, now.Add(time.Duration(n)*time.Second), nil)
		if n == 2 {
			waiting, e := s.AutoFlowCompilation(ctx, f.ID, now)
			if e != nil || waiting.Created || waiting.MissingDurationSec <= 0 || waiting.ReadyDurationSec < 62.09 {
				t.Fatal(waiting, e)
			}
		}
	}
	if err = s.TickTwitchFlows(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	comp, err := s.AutoFlowCompilation(ctx, f.ID, now)
	if err != nil || comp.Compilation == nil || comp.Reason != "daily_draft_exists" {
		t.Fatal(comp, err)
	}
	completeRender(t, s, comp.Compilation.RevisionID, now.Add(time.Minute), nil)
	if err = s.TickTwitchFlows(ctx, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = s.TickTwitchFlows(ctx, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.ListTwitchImports(ctx, true, ""); err != nil || len(pending) != 0 {
		t.Fatal("approved and used sources remained in queue", pending, err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM publications`).Scan(&count); err != nil || count != 5 {
		t.Fatal("scheduler duplicated plans", count, err)
	}
	var shorts int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE o.format='short' AND p.publish_at::date=$1::date`, now.Format("2006-01-02")).Scan(&shorts); err != nil || shorts != 2 {
		t.Fatal(shorts, err)
	}
	// The scheduler uses an injected clock while next_attempt_at defaults to the
	// database clock. Make eligibility explicit rather than racing those clocks.
	if _, err = s.pool.Exec(ctx, `UPDATE publications SET next_attempt_at=$1`, now); err != nil {
		t.Fatal(err)
	}
	firstAt := now.Add(time.Hour)
	if err = s.PreparePublications(ctx, firstAt, 50); err != nil {
		t.Fatal(err)
	}
	var first, firstAttempt string
	var firstPID int64
	var version int
	if err = s.pool.QueryRow(ctx, `SELECT a.id,p.clip_id,p.id,p.version FROM publication_attempts a JOIN publications p ON p.id=a.publication_id WHERE a.status='active' AND p.status='queued' ORDER BY p.publish_at LIMIT 1`).Scan(&firstAttempt, &first, &firstPID, &version); err != nil {
		t.Fatal(err)
	}
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: w.Command{Kind: w.KindPublish, AttemptID: firstAttempt, PublicationID: firstPID, ExpectedVersion: version}, WorkerID: "started"}, firstAt)
	if err != nil || !lease.Granted {
		t.Fatal(lease, err)
	}
	secondAt := now.Add(4 * time.Hour)
	if err = s.PreparePublications(ctx, secondAt, 50); err != nil {
		t.Fatal(err)
	}
	var queuedAttempt string
	var queuedPID int64
	var queuedVersion int
	if err = s.pool.QueryRow(ctx, `SELECT a.id,p.id,p.version FROM publication_attempts a JOIN publications p ON p.id=a.publication_id WHERE a.status='active' AND p.status='queued' AND p.started_at IS NULL LIMIT 1`).Scan(&queuedAttempt, &queuedPID, &queuedVersion); err != nil {
		t.Fatal(err)
	}
	f.AutoPublish = false
	f, err = s.SaveTwitchFlow(ctx, f, false, secondAt)
	if err != nil {
		t.Fatal(err)
	}
	denied, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: w.Command{Kind: w.KindPublish, AttemptID: queuedAttempt, PublicationID: queuedPID, ExpectedVersion: queuedVersion}, WorkerID: "late"}, secondAt)
	if err != nil || denied.Granted {
		t.Fatal(denied, err)
	}
	var state string
	var held bool
	if err = s.pool.QueryRow(ctx, `SELECT status,auto_hold FROM publications WHERE id=$1`, firstPID).Scan(&state, &held); err != nil || held || state == "on_hold" {
		t.Fatal("started publication changed on OFF", state, held, err)
	}
	f.AutoPublish = true
	f, err = s.SaveTwitchFlow(ctx, f, false, secondAt)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.TickTwitchFlows(ctx, secondAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.CompilationMinDurationSec = 50
	f.CompilationMaxDurationSec = 60
	f, err = s.SaveTwitchFlow(ctx, f, false, secondAt)
	if err != nil {
		t.Fatal(err)
	}
	existing := comp.Compilation
	updated, err := s.UpdateCompilation(ctx, existing.ClipID, domain.CompilationUpdate{ExpectedVersion: existing.Version, CompilationRequest: twitch.CompilationRequest{FlowID: f.ID, Title: "Ручная правка", Items: existing.Items}}, secondAt)
	if err != nil {
		t.Fatal("frozen compilation bounds lost", err)
	}
	if updated.FlowConfig == nil || updated.FlowConfig.CompilationMaxDurationSec != 84 {
		t.Fatal(updated)
	}
	completeRender(t, s, updated.RevisionID, secondAt, nil)
	if err = s.TickTwitchFlows(ctx, secondAt.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT p.status FROM publications p WHERE p.clip_id=$1`, updated.ClipID).Scan(&state); err != nil || state != "on_hold" {
		t.Fatal("manual revision auto-published", state, err)
	}
	if err = s.RestoreRevision(ctx, updated.ClipID, updated.Version, existing.RevisionID); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM twitch_compilation_items ci JOIN clips c ON c.latest_revision_id=ci.compilation_revision_id WHERE c.clip_id=$1`, updated.ClipID).Scan(&count); err != nil || count != 4 {
		t.Fatal("restored source pins lost", count, err)
	}
}
