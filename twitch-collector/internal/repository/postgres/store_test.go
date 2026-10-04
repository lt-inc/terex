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
	// Enforce ten tracked channels in the same transaction as additions.
	for i := 3; i <= 10; i++ {
		if _, err = s.CreateChannel(ctx, domain.Channel{BroadcasterID: string(rune('a' + i)), Login: "test", DisplayName: "Test"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CreateChannel(ctx, domain.Channel{BroadcasterID: "eleventh", Login: "extra", DisplayName: "Extra"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("channel cap not enforced", err)
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
