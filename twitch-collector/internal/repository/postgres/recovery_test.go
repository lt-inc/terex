package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

func TestApprovedRecoveryAdmissionAndUploadedPhaseSurviveLostResponse(t *testing.T) {
	dsn := os.Getenv("COLLECTOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable collector test database")
	}
	if !strings.Contains(dsn, "_test") {
		t.Fatal("test database required")
	}
	ctx, now := context.Background(), time.Now().UTC()
	s, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.pool.Exec(ctx, `DROP SCHEMA IF EXISTS twitch_collector CASCADE`); err != nil {
		t.Fatal(err)
	}
	defer s.pool.Exec(ctx, `DROP SCHEMA twitch_collector CASCADE`)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateChannel(ctx, domain.Channel{BroadcasterID: "1", Login: "streamer", DisplayName: "Streamer"})
	if err != nil {
		t.Fatal(err)
	}
	f := twitch.Flow{ID: "recovery-flow", Version: 1, Enabled: true, SourceMode: "channels", BroadcasterIDs: []string{"1"}, MinDurationSec: 5, MaxDurationSec: 180, WindowHours: 72, MaxSourceAgeHours: 72}
	if err = s.SyncFlows(ctx, []twitch.Flow{f}); err != nil {
		t.Fatal(err)
	}
	clip := domain.Clip{ID: "RecoveryClip", ChannelID: channel.ID, BroadcasterID: "1", Duration: 30, ViewCount: 100, CreatedAt: now.Add(-2 * time.Hour), Language: "ru", LanguageSource: "clip"}
	if err = s.UpsertClips(ctx, []domain.Clip{clip}); err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveFlow(ctx, f, []domain.Clip{clip}); err != nil {
		t.Fatal(err)
	}
	g := twitch.DownloadGrant{ClipID: clip.ID, ImportID: "recovery-import", FlowID: f.ID, FlowVersion: 1, AdmissionSequence: 1, Source: twitch.Source{ClipID: clip.ID, DurationSec: 30}}
	if _, err = s.QueueAdmissions(ctx, []twitch.DownloadGrant{g}, now); err != nil {
		t.Fatal(err)
	}
	task, err := s.Claim(ctx, time.Minute)
	if err != nil || task == nil {
		t.Fatal(task, err)
	}
	if err = s.FailWithCode(ctx, *task, "revalidation", "settings changed", "flow_version_changed", nil); err != nil {
		t.Fatal(err)
	}
	f.Version = 2
	if err = s.SyncFlows(ctx, []twitch.Flow{f}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.RecoverableImports(ctx, 0)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	failure := rows[0]
	if failure.FlowVersion != 2 || failure.FailureToken == "" {
		t.Fatal(failure)
	}
	// A normal grant cannot resurrect a failed task without a main-be receipt.
	newGrant := g
	newGrant.AdmissionSequence = 2
	newGrant.FlowVersion = 2
	if n, e := s.QueueAdmissions(ctx, []twitch.DownloadGrant{newGrant}, now); e != nil || n != 0 {
		t.Fatal("unapproved failed task resurrected", n, e)
	}
	newGrant.RevalidationToken = failure.FailureToken
	if n, e := s.QueueAdmissions(ctx, []twitch.DownloadGrant{newGrant}, now); e != nil || n != 1 {
		t.Fatal("lost receipt response blocked approved grant", n, e)
	}
	receipt := twitch.RevalidationReceipt{AdmissionSequence: 1, FailureToken: failure.FailureToken, FlowID: f.ID, FlowVersion: 2, State: "discovered"}
	if err = s.ApplyRevalidation(ctx, failure, receipt); err != nil {
		t.Fatal(err)
	}
	if n, e := s.QueueAdmissions(ctx, []twitch.DownloadGrant{g}, now); e != nil || n != 0 {
		t.Fatal("late old grant replaced current sequence", n, e)
	}
	task, err = s.Claim(ctx, time.Minute)
	if err != nil || task == nil || task.Reservation.AdmissionSequence != 2 || task.Attempt != 1 {
		t.Fatal(task, err)
	}
	media := domain.UploadedMedia{SHA256: strings.Repeat("a", 64), SizeBytes: 100, Phase: "uploaded"}
	if err = s.SaveUploaded(ctx, *task, media); err != nil {
		t.Fatal(err)
	}
	retry := now.Add(-time.Second)
	if err = s.FailWithCode(ctx, *task, "network", "verification unavailable", "", &retry); err != nil {
		t.Fatal(err)
	}
	other, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	resumed, err := other.Claim(ctx, time.Minute)
	if err != nil || resumed == nil || resumed.Uploaded == nil || resumed.Uploaded.SHA256 != media.SHA256 {
		t.Fatal("uploaded bytes lost after restart", resumed, err)
	}
	if err = other.FailWithCode(ctx, *resumed, "import", "new failure", "invalid_media", nil); err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyRevalidation(ctx, failure, receipt); err != nil {
		t.Fatal(err)
	}
	current, err := other.FailedForRetry(ctx, clip.ID)
	if err != nil || current.FailureToken == failure.FailureToken {
		t.Fatal("old receipt overwrote a new failure", current, err)
	}
}
