package postgres

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

func TestPostgresReservationsRecoveryAndFencing(t *testing.T) {
	dsn := os.Getenv("COLLECTOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable COLLECTOR_TEST_DATABASE_URL for database integration")
	}
	parsed, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(parsed.ConnConfig.Database, "_test") {
		t.Fatal("integration database must have _test suffix")
	}
	ctx := context.Background()
	s, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.pool.Exec(ctx, "DROP SCHEMA IF EXISTS twitch_collector CASCADE"); err != nil {
		t.Fatal(err)
	}
	defer s.pool.Exec(ctx, "DROP SCHEMA twitch_collector CASCADE")
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("migration not idempotent", err)
	}
	a, err := s.CreateChannel(ctx, domain.Channel{BroadcasterID: "1", Login: "a", DisplayName: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateChannel(ctx, domain.Channel{BroadcasterID: "2", Login: "b", DisplayName: "B"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = s.ChannelChecked(ctx, a.ID, now, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.ChannelChecked(ctx, a.ID, now.Add(time.Hour), "Twitch unavailable"); err != nil {
		t.Fatal(err)
	}
	checked, err := s.GetChannel(ctx, a.ID)
	if err != nil || checked.LastCheckedAt == nil || checked.LastCheckedAt.Sub(now) > time.Millisecond || checked.LastError != "Twitch unavailable" {
		t.Fatal("failed check replaced last successful timestamp", checked, err)
	}
	clips := []domain.Clip{{ID: "a1", ChannelID: a.ID, BroadcasterID: "1", BroadcasterName: "a", CreatedAt: now.Add(-2 * time.Hour), Duration: 20, ViewCount: 500}, {ID: "b1", ChannelID: b.ID, BroadcasterID: "2", BroadcasterName: "b", CreatedAt: now.Add(-2 * time.Hour), Duration: 20, ViewCount: 400}, {ID: "a2", ChannelID: a.ID, BroadcasterID: "1", BroadcasterName: "a", CreatedAt: now.Add(-2 * time.Hour), Duration: 20, ViewCount: 300}, {ID: "b2", ChannelID: b.ID, BroadcasterID: "2", BroadcasterName: "b", CreatedAt: now.Add(-2 * time.Hour), Duration: 20, ViewCount: 200}}
	// Real Twitch clips commonly have fractional VOD offsets. The overlap query
	// must prepare successfully and preserve deduplication before queuing a file.
	for i := range clips {
		offset := 10.25 + float64(i)*40
		clips[i].VideoID = "vod-" + clips[i].BroadcasterID
		clips[i].VODOffset = &offset
	}
	if err = s.UpsertClips(ctx, clips); err != nil {
		t.Fatal(err)
	}
	settings := twitch.DefaultSettings()
	settings.DailyDownloads = 2
	settings.PerChannelDailyDownloads = 1
	// Concurrent checks must not exceed either quota or create duplicate tasks.
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, e := s.Reserve(ctx, clips, settings, now)
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				t.Error(e)
			}
			total += n
		}()
	}
	wg.Wait()
	if total != 2 {
		t.Fatalf("reserved %d", total)
	}
	overlap := clips[0]
	overlap.ID = "a-overlap"
	offset := *overlap.VODOffset + 5.5
	overlap.VODOffset = &offset
	if err = s.UpsertClips(ctx, []domain.Clip{overlap}); err != nil {
		t.Fatal(err)
	}
	looseQuotas := settings
	looseQuotas.DailyDownloads, looseQuotas.PerChannelDailyDownloads = 10, 10
	if n, e := s.Reserve(ctx, []domain.Clip{overlap}, looseQuotas, now); e != nil || n != 0 {
		t.Fatal("overlapping VOD source was queued", n, e)
	}
	task, err := s.Claim(ctx, 30*time.Second)
	if err != nil || task == nil || task.Attempt != 1 {
		t.Fatal(task, err)
	}
	if err = s.Renew(ctx, *task, time.Minute); err != nil {
		t.Fatal(err)
	}
	// Simulate a crashed process and expiry, then recover with a new fence token.
	if _, err = s.pool.Exec(ctx, "UPDATE twitch_collector.download_tasks SET lease_expires_at=now()-interval '1 second' WHERE clip_id=$1", task.Clip.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.Claim(ctx, time.Minute)
	if err != nil || recovered == nil || recovered.Clip.ID != task.Clip.ID || recovered.Attempt != 2 || recovered.Token == task.Token {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	for name, fn := range map[string]func() error{"complete": func() error { return s.Complete(ctx, *task, "stale", "hash") }, "fail": func() error { return s.Fail(ctx, *task, "network", "stale", nil) }, "renew": func() error { return s.Renew(ctx, *task, time.Minute) }, "defer": func() error { return s.Defer(ctx, *task, now) }} {
		if err = fn(); !errors.Is(err, domain.ErrLeaseLost) {
			t.Errorf("stale %s accepted: %v", name, err)
		}
	}
	if err = s.Complete(ctx, *recovered, "terex-id", "abc"); err != nil {
		t.Fatal(err)
	}
	found, err := s.HasSHA256(ctx, "abc", "other")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	pausedTask, err := s.Claim(ctx, time.Minute)
	if err != nil || pausedTask == nil {
		t.Fatal(pausedTask, err)
	}
	pause := true
	if _, err = s.SetChannel(ctx, pausedTask.Clip.ChannelID, &pause, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(ctx, *pausedTask, "bad", "bad"); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatal("paused completion accepted", err)
	}
	if err = s.Defer(ctx, *pausedTask, now); err != nil {
		t.Fatal(err)
	}
	if task, err = s.Claim(ctx, time.Minute); err != nil || task != nil {
		t.Fatal("paused task claimed", task, err)
	}
	pause = false
	if _, err = s.SetChannel(ctx, pausedTask.Clip.ChannelID, &pause, false); err != nil {
		t.Fatal(err)
	}
	task, err = s.Claim(ctx, time.Minute)
	if err != nil || task == nil || task.Attempt != 1 {
		t.Fatal("pause consumed attempt", task, err)
	}
	retry := now.Add(5 * time.Minute)
	if err = s.Fail(ctx, *task, "network", "temporary", &retry); err != nil {
		t.Fatal(err)
	}
	if task, err = s.Claim(ctx, time.Minute); err != nil || task != nil {
		t.Fatal("retry ran before cooldown", task, err)
	}
	// The next local calendar day can reserve new IDs; old IDs remain deduplicated.
	n, err := s.Reserve(ctx, clips, settings, now.Add(24*time.Hour))
	if err != nil || n != 2 {
		t.Fatal("calendar-day quota did not reset", n, err)
	}
	// The catalogue is shared by flows and category discovery; it has no ten-channel cap.
	// Simulate upgrading an existing two-channel collector before new flow discovery.
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_collector.state SET legacy_broadcaster_ids=NULL`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 3; i <= 10; i++ {
		if _, err = s.CreateChannel(ctx, domain.Channel{BroadcasterID: string(rune('a' + i)), Login: "test", DisplayName: "Test"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CreateChannel(ctx, domain.Channel{BroadcasterID: "eleventh", Login: "extra", DisplayName: "Extra"}); err != nil {
		t.Fatal("shared catalogue unexpectedly capped", err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if ids, e := s.LegacyBroadcasterIDs(ctx); e != nil || len(ids) != 2 {
		t.Fatal("new flow sources leaked into the legacy flow", ids, e)
	}

	f1 := twitch.Flow{ID: "flow-one", Version: 1, Enabled: true, MinViews: 10, WindowHours: 72, MinAgeMinutes: 60, MinDurationSec: 5, MaxDurationSec: 60}
	f2 := f1
	f2.ID = "flow-two"
	if err = s.SyncFlows(ctx, []twitch.Flow{f1, f2}); err != nil {
		t.Fatal(err)
	}
	shared := domain.Clip{ID: "shared-flow-clip", ChannelID: a.ID, BroadcasterID: a.BroadcasterID, BroadcasterName: a.Login, Duration: 20, ViewCount: 900, CreatedAt: now.Add(-2 * time.Hour), FlowID: f1.ID, FlowVersion: 1}
	if err = s.UpsertClips(ctx, []domain.Clip{shared}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []twitch.Flow{f1, f2} {
		if err = s.ObserveFlow(ctx, f, []domain.Clip{shared}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err = s.Reserve(ctx, []domain.Clip{shared, shared}, looseQuotas, now); err != nil || n != 1 {
		t.Fatal("shared source downloaded twice", n, err)
	}
	task, err = s.Claim(ctx, time.Minute)
	if err != nil || task == nil || task.Clip.ID != shared.ID {
		t.Fatal("flow task not claimed", task, err)
	}
	reservation := twitch.Import{ImportID: "saved-import", ReservationID: "saved-reservation", UploadGeneration: 2, FlowID: f2.ID, FlowVersion: 1}
	f2.Version = 2
	if err = s.SyncFlows(ctx, []twitch.Flow{f1, f2}); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveReservation(ctx, *task, reservation); err != nil {
		t.Fatal(err)
	}
	f1.Enabled = false
	f1.Version = 2
	if err = s.SyncFlows(ctx, []twitch.Flow{f1, f2}); err != nil {
		t.Fatal(err)
	}
	if active, e := s.HasActiveConsumer(ctx, shared.ID); e != nil || !active {
		t.Fatal("pausing one flow stopped shared download", active, e)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks SET lease_expires_at=now()-interval '1 second' WHERE clip_id=$1`, shared.ID); err != nil {
		t.Fatal(err)
	}
	task, err = s.Claim(ctx, time.Minute)
	if err != nil || task == nil || task.Reservation.ReservationID != reservation.ReservationID || task.Reservation.UploadGeneration != 2 || task.FlowID != f2.ID || task.FlowVersion != 1 {
		t.Fatal("restart lost import fence", task, err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks SET attempts=3,lease_expires_at=now()-interval '1 second' WHERE clip_id=$1`, shared.ID); err != nil {
		t.Fatal(err)
	}
	if task, err = s.Claim(ctx, time.Minute); err != nil || task != nil {
		t.Fatal("exhausted task reclaimed", task, err)
	}
	var exhaustedState string
	if err = s.pool.QueryRow(ctx, `SELECT state FROM twitch_collector.clips WHERE clip_id=$1`, shared.ID).Scan(&exhaustedState); err != nil || exhaustedState != "failed" {
		t.Fatal("empty claim rolled back exhausted state", exhaustedState, err)
	}
	admitted := shared
	admitted.ID = "admitted-before-upload"
	if err = s.UpsertClips(ctx, []domain.Clip{admitted}); err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveFlow(ctx, f2, []domain.Clip{admitted}); err != nil {
		t.Fatal(err)
	}
	grant := twitch.DownloadGrant{ClipID: admitted.ID, ImportID: "admitted-import", FlowID: f2.ID, FlowVersion: f2.Version, Format: "compilation", Source: twitch.Source{ClipID: admitted.ID, Language: "en", LanguageSource: "clip", DurationSec: 20}}
	if n, err = s.QueueAdmissions(ctx, []twitch.DownloadGrant{grant}, now); err != nil || n != 1 {
		t.Fatal("admission was not queued", n, err)
	}
	task, err = s.Claim(ctx, time.Minute)
	if err != nil || task == nil || task.Reservation.ImportID != grant.ImportID || task.Reservation.UploadGeneration != 0 || task.Reservation.Source.LanguageSource != "clip" {
		t.Fatal("admission lost its pre-upload identity", task, err)
	}
	if err = s.Fail(ctx, *task, "import", "filters rejected before first upload", nil); err != nil {
		t.Fatal(err)
	}
	failures, err := s.FailedImports(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	failureFound := false
	for _, failure := range failures {
		if failure.Reservation.ImportID == grant.ImportID {
			failureFound = true
			if err = s.RetryClip(ctx, failure.ClipID); err != nil {
				t.Fatal(err)
			}
			task, err = s.Claim(ctx, time.Minute)
			if err != nil || task == nil {
				t.Fatal("failed admission could not retry", task, err)
			}
			if err = s.Fail(ctx, *task, "import", "new terminal failure", nil); err != nil {
				t.Fatal(err)
			}
			if err = s.FailedImportSynced(ctx, failure); err != nil {
				t.Fatal(err)
			}
			var prematurelySynced bool
			if err = s.pool.QueryRow(ctx, `SELECT failure_synced_at IS NOT NULL FROM twitch_collector.download_tasks WHERE clip_id=$1`, failure.ClipID).Scan(&prematurelySynced); err != nil || prematurelySynced {
				t.Fatal("late acknowledgement swallowed a new terminal failure", prematurelySynced, err)
			}
		}
	}
	if !failureFound {
		t.Fatal("terminal failure lost its durable generation-zero release")
	}
	// A denied preparation never starts yt-dlp and must not consume the daily
	// download quota or prevent another flow from using its available slot.
	if _, err = s.pool.Exec(ctx, `UPDATE twitch_collector.clips SET reserved_at=$1`, now.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	candidates := []domain.Clip{
		{ID: "capacity-wait", ChannelID: a.ID, BroadcasterID: "1", CreatedAt: now.Add(-2 * time.Hour), Duration: 20},
		{ID: "capacity-available", ChannelID: a.ID, BroadcasterID: "1", CreatedAt: now.Add(-2 * time.Hour), Duration: 20},
	}
	if err = s.UpsertClips(ctx, candidates); err != nil {
		t.Fatal(err)
	}
	settings.DailyDownloads, settings.PerChannelDailyDownloads = 1, 1
	if n, err = s.Reserve(ctx, candidates[:1], settings, now); err != nil || n != 1 {
		t.Fatal("capacity fixture not reserved", n, err)
	}
	task, err = s.Claim(ctx, time.Minute)
	if err != nil || task == nil || task.Clip.ID != candidates[0].ID {
		t.Fatal("capacity fixture not claimed", task, err)
	}
	if err = s.DeferForCapacity(ctx, *task, now.Add(time.Hour), "calendar full"); err != nil {
		t.Fatal(err)
	}
	if n, err = s.Reserve(ctx, candidates[1:], settings, now); err != nil || n != 1 {
		t.Fatal("waiting for capacity consumed download quota", n, err)
	}
}

func TestCollectorPrincipalCannotReadBackendTables(t *testing.T) {
	dsn := os.Getenv("COLLECTOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set disposable COLLECTOR_TEST_DATABASE_URL")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatal("integration database must have _test suffix")
	}
	ctx := context.Background()
	admin, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	role := "collector_test_" + newID()
	// Synthetic per-run credentials let the same boundary test run against
	// password-authenticated CI PostgreSQL and local trust-only fixtures.
	password := newID() + newID()
	quotedPassword := "'" + strings.ReplaceAll(password, "'", "''") + "'"
	quoted := pgx.Identifier{role}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE ROLE "+quoted+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT PASSWORD "+quotedPassword); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP ROLE "+quoted)
	if _, err = admin.Exec(ctx, "CREATE TABLE public.collector_boundary_canary(value text)"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP TABLE public.collector_boundary_canary")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA twitch_collector AUTHORIZATION "+quoted); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA twitch_collector CASCADE")
	config.ConnConfig.User = role
	config.ConnConfig.Password = password
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := &Store{pool: pool}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal("own-schema migration failed under restricted principal", err)
	}
	if _, err = store.CreateChannel(ctx, domain.Channel{BroadcasterID: "1", Login: "name", DisplayName: "Name"}); err != nil {
		t.Fatal("own-schema writes denied", err)
	}
	if _, err = pool.Exec(ctx, "SELECT * FROM public.collector_boundary_canary"); err == nil {
		t.Fatal("collector principal could read backend table")
	}
	if _, err = pool.Exec(ctx, "CREATE TABLE public.collector_forbidden(value text)"); err == nil {
		admin.Exec(ctx, "DROP TABLE public.collector_forbidden")
		t.Fatal("collector principal could write public schema")
	}
}
