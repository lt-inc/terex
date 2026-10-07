package store

import (
	"context"
	"os"
	"testing"
	"time"

	"terex/proto/workflow"
)

// Needs a disposable database: it creates public.accounts if missing and drops yt_stats.
// Set STATS_TEST_DATABASE_URL explicitly; never point it at production.
func TestStoreLifecycle(t *testing.T) {
	dsn := os.Getenv("STATS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("STATS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, q := range []string{
		`DROP SCHEMA IF EXISTS yt_stats CASCADE`,
		`CREATE TABLE IF NOT EXISTS public.accounts(id bigserial PRIMARY KEY, platform text NOT NULL, name text NOT NULL, remote_account_id text, connection_state text NOT NULL DEFAULT 'disconnected', credential_ref jsonb NOT NULL DEFAULT '{}')`,
		`TRUNCATE public.accounts`,
		`INSERT INTO public.accounts(platform,name,remote_account_id,connection_state,credential_ref) VALUES
			('youtube','Main','UC1','connected','{"bucket":"credentials","object":"google/1.json"}'),
			('youtube','NoChannel',NULL,'connected','{}'),('tiktok','TT','tt1','connected','{"object":"x"}'),
			('youtube','NeedsReauth','UC3','needs_reauth','{"bucket":"credentials","object":"google/3.json"}'),
			('youtube','Gone','UC2','connected','{"bucket":"credentials","object":"google/2.json"}')`,
	} {
		if _, err = s.pool.Exec(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	for range 2 { // migration must be repeatable
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	accs, err := s.Accounts(ctx)
	if err != nil || len(accs) != 2 || accs[0].ChannelID != "UC1" || accs[0].CredentialRef.Object != "google/1.json" {
		t.Fatalf("only connected youtube accounts with a credential are tracked: %v %+v", err, accs)
	}

	now := time.Now().UTC().Truncate(time.Second)
	subs := int64(10)
	for _, a := range accs {
		if err = s.SaveChannel(ctx, a, workflow.ChannelStats{ChannelID: a.ChannelID, Title: a.Name, Subscribers: &subs, Views: 100, Videos: 2}, now); err != nil {
			t.Fatal(err)
		}
	}
	// the same snapshot twice and a hidden-likes video must not fail
	likes := int64(3)
	vs := []workflow.VideoStats{
		{ID: "v1", Title: "one", PublishedAt: now.Add(-48 * time.Hour), Views: 50, Likes: &likes},
		{ID: "v2", Title: "two", PublishedAt: now.Add(-24 * time.Hour), Views: 5},
	}
	for range 2 {
		if err = s.SaveVideos(ctx, "UC1", vs, now); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string) (n int) {
		if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return
	}
	if n := count(`SELECT count(*) FROM yt_stats.video_snapshots`); n != 2 {
		t.Errorf("snapshots=%d want 2 (duplicate ts ignored)", n)
	}

	// thinning keeps every recent snapshot and one per day for old ones
	for _, h := range []int{-30*24 + 1, -30*24 + 2, -30*24 + 3} {
		if err = s.SaveVideos(ctx, "UC1", vs[:1], now.Add(time.Duration(h)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Thin(ctx, 14); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM yt_stats.video_snapshots WHERE video_id='v1'`); n != 2 {
		t.Errorf("v1 snapshots after thinning=%d want 2 (1 recent + 1 for the old day)", n)
	}

	// removing an account drops its channel together with history
	if _, err = s.pool.Exec(ctx, `DELETE FROM public.accounts WHERE remote_account_id='UC2'`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DropRemoved(ctx); err != nil || n != 1 {
		t.Fatalf("DropRemoved n=%d err=%v", n, err)
	}
	if err = s.SaveRun(ctx, Run{Started: now, Finished: now, Channels: 1}); err != nil {
		t.Fatal(err)
	}
}
