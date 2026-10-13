package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	w "terex/proto/workflow"
	"testing"
	"time"
)

func tikTokTestAccount(t *testing.T, s *Store) int64 {
	t.Helper()
	a, err := s.CreateAccount(context.Background(), domain.Account{Platform: "tiktok", Name: "TikTok test"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveTikTokConnection(context.Background(), domain.TikTokConnection{AccountID: a.ID, Status: "connected", RemoteAccountID: "123456789", Username: "test", SessionRef: "session/test", SessionVersion: 1}); err != nil {
		t.Fatal(err)
	}
	return a.ID
}
func TestTikTokDestinationsShareAdmissionMediaAndRender(t *testing.T) {
	s, yt, settings := twitchFixture(t)
	ctx := context.Background()
	clock := time.Now().UTC()
	now := time.Date(clock.Year(), clock.Month(), clock.Day(), 8, 0, 0, 0, time.UTC)
	settings.AdmissionMode = "enforce"
	if err := s.SaveTwitchSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	tt := tikTokTestAccount(t, s)
	f := defaultFlow()
	f.Enabled = true
	f.CompilationsEnabled = false
	f.BroadcasterIDs = []string{"123"}
	f.Timezone = "UTC"
	f.Destinations = []twitch.Destination{{Provider: "youtube", AccountID: yt, Enabled: true, AutoPublish: true, Timezone: "UTC", ShortsSlots: []string{"09:00"}, DailyShorts: 3, ScheduleHorizonHours: 24, Visibility: "public"}, {Provider: "tiktok", AccountID: tt, Enabled: true, AutoPublish: true, Timezone: "UTC", ShortsSlots: []string{"10:00"}, DailyShorts: 3, ScheduleHorizonHours: 24, Visibility: "private"}}
	var err error
	f, err = s.SaveTwitchFlow(ctx, f, true, now)
	if err != nil {
		t.Fatal(err)
	}
	src := flowSource("dual-target", "123", 20, now)
	if _, err = s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: []twitch.Source{src}}, now); err != nil {
		t.Fatal(err)
	}
	grants, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: 10}, now)
	if err != nil || len(grants.Grants) != 1 {
		t.Fatal("physical download duplicated", grants, err)
	}
	im := finishFlow(t, s, reserveFlow(t, s, f, src, now), 20, "", now)
	completeRender(t, s, im.RevisionID, now, nil)
	if err = s.TickTwitchFlows(ctx, now); err != nil {
		t.Fatal(err)
	}
	ps := pubs(t, s)
	if len(ps) != 2 || ps[0].RevisionID != ps[1].RevisionID {
		t.Fatal("shared revision missing", ps)
	}
	var downloads, outputs, attempts int
	if err = s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM twitch_download_reservations),(SELECT count(*) FROM twitch_outputs),(SELECT count(*) FROM render_attempts)`).Scan(&downloads, &outputs, &attempts); err != nil || downloads != 1 || outputs != 1 || attempts != 1 {
		t.Fatal(downloads, outputs, attempts, err)
	}
	if ps[0].Platform != "youtube" || ps[1].Platform != "tiktok" || ps[1].Visibility != "private" || ps[1].Caption == "" || strings.Contains(ps[1].Caption, "#shorts") {
		t.Fatal("target metadata leaked", ps)
	}
	// A target hold cannot destroy the other destination's future calendar.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = lockTwitch(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = holdPolicyPublication(ctx, tx, domain.TwitchPolicyViolation{PublicationID: ps[1].ID, ClipID: im.ClipID, Code: "calendar_horizon", Message: "TikTok target held"}, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	ytPub, err := s.GetPublicationRecord(ctx, ps[0].ID)
	if err != nil || ytPub.Status != "scheduled" {
		t.Fatal("TikTok hold affected YouTube", ytPub, err)
	}
	// Adding a destination later never fans out historical output.
	f.Destinations[1].Enabled = false
	f, err = s.SaveTwitchFlow(ctx, f, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.TickTwitchFlows(ctx, now); err != nil {
		t.Fatal(err)
	}
	if len(pubs(t, s)) != 2 {
		t.Fatal("history rescheduled")
	}
}

func TestTikTokOnlyDispatchSurvivesWaitingAndFencesUnknownRetry(t *testing.T) {
	s, _, _ := twitchFixture(t)
	ctx := context.Background()
	clock := time.Now().UTC()
	now := time.Date(clock.Year(), clock.Month(), clock.Day(), 8, 0, 0, 0, time.UTC)
	tt := tikTokTestAccount(t, s)
	f := defaultFlow()
	f.Enabled = true
	f.CompilationsEnabled = false
	f.BroadcasterIDs = []string{"123"}
	f.Timezone = "UTC"
	f.Destinations = []twitch.Destination{{Provider: "tiktok", AccountID: tt, Enabled: true, AutoPublish: true, Timezone: "UTC", ShortsSlots: []string{"09:00"}, DailyShorts: 3, ScheduleHorizonHours: 24, Visibility: "private"}}
	var err error
	f, err = s.SaveTwitchFlow(ctx, f, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if f.YouTubeChannelID != "" {
		t.Fatal("invented a YouTube destination", f)
	}
	im := finishFlow(t, s, reserveFlow(t, s, f, flowSource("tiktok-only", "123", 20, now), now), 20, "", now)
	completeRender(t, s, im.RevisionID, now, nil)
	if _, err = s.pool.Exec(ctx, `UPDATE clip_revisions SET media_sha256=$2,media_size_bytes=100 WHERE id=$1`, im.RevisionID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err = s.TickTwitchFlows(ctx, now); err != nil {
		t.Fatal(err)
	}
	p := pubs(t, s)[0]
	due := p.PublishAt
	if err = s.PreparePublications(ctx, due, 10); err != nil {
		t.Fatal(err)
	}
	var attempt string
	var execution *time.Time
	if err = s.pool.QueryRow(ctx, `SELECT id,execution_started_at FROM publication_attempts WHERE publication_id=$1 AND status='active'`, p.ID).Scan(&attempt, &execution); err != nil || execution != nil {
		t.Fatal("execution lease started in queue", err)
	}
	if err = s.PreparePublications(ctx, due.Add(16*time.Minute), 10); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM publication_attempts WHERE publication_id=$1`, p.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("waiting worker churned attempts", count, err)
	}
	lease, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: w.Command{Kind: "publish", PublicationID: p.ID, AttemptID: attempt, ExpectedVersion: p.Version}, WorkerID: "tiktok-worker"}, due.Add(16*time.Minute))
	if err != nil || !lease.Granted || lease.Publication.SessionVersion != 1 {
		t.Fatal(lease, err)
	}
	lr := w.LeaseRequest{AttemptID: attempt, LeaseToken: lease.LeaseToken, ExpectedVersion: p.Version}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM tiktok_worker_operations WHERE attempt_id=$1`, attempt).Scan(&count); err != nil || count != 0 {
		t.Fatal("claim without acknowledged physical start registered an operation", count, err)
	}
	accepted, err := s.CheckpointWorkflow(ctx, &w.CheckpointRequest{LeaseRequest: lr, Checkpoint: w.Checkpoint{State: "publishing", FinalRequestStarted: true, ProviderCheckpoint: map[string]any{"creation_id": "transfer-only"}}}, due.Add(16*time.Minute))
	if err != nil || !accepted {
		t.Fatal(accepted, err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM tiktok_worker_operations WHERE attempt_id=$1 AND state='active'`, attempt).Scan(&count); err != nil || count != 1 {
		t.Fatal("acknowledged operation missing from reset registry", count, err)
	}
	if released, err := s.ReleaseTikTokOperation(ctx, attempt, "wrong-token", due.Add(16*time.Minute)); err != nil || released {
		t.Fatal("unowned release accepted", released, err)
	}
	if err = s.PreparePublications(ctx, due.Add(19*time.Minute), 10); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetPublicationRecord(ctx, p.ID)
	if err != nil || p.Status != "needs_attention" {
		t.Fatal(p, err)
	}
	if err = s.ActPublication(ctx, p.ID, "retry", p.Version, due.Add(19*time.Minute)); err == nil {
		t.Fatal("blind retry accepted")
	}
	if released, err := s.ReleaseTikTokOperation(ctx, attempt, lease.LeaseToken, due.Add(19*time.Minute)); err != nil || !released {
		t.Fatal("worker release", released, err)
	}
	if err = s.RetryTikTokAfterAbsence(ctx, p.ID, p.Version, due.Add(19*time.Minute)); err != nil {
		t.Fatal(err)
	}
	p, err = s.GetPublicationRecord(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PreparePublications(ctx, due.Add(19*time.Minute), 10); err != nil {
		t.Fatal(err)
	}
	var cmd w.Command
	if err = s.pool.QueryRow(ctx, `SELECT id,publication_version FROM publication_attempts WHERE publication_id=$1 AND status='active'`, p.ID).Scan(&cmd.AttemptID, &cmd.ExpectedVersion); err != nil {
		t.Fatal(err)
	}
	cmd.Kind = "publish"
	cmd.PublicationID = p.ID
	retry, err := s.ClaimWorkflow(ctx, &w.ClaimRequest{Command: cmd, WorkerID: "tiktok-worker-2"}, due.Add(19*time.Minute))
	if err != nil || !retry.Granted || !retry.Publication.RetryAuthorized || retry.Publication.Checkpoint.FinalRequestStarted {
		t.Fatal("explicit new version did not fence old journal", retry, err)
	}
	cp, _ := json.Marshal(retry.Publication.Checkpoint)
	if strings.Contains(string(cp), "transfer-only") {
		t.Fatal("old upload checkpoint reused")
	}
}
