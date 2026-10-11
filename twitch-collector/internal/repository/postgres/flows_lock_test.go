package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

// SyncFlows updates every consumers row of a flow in one statement while
// ObserveFlow upserts them one by one. Without the per-flow advisory lock the
// two take row locks in different orders and Postgres reports a deadlock.
func TestSyncFlowsAndObserveFlowDoNotDeadlock(t *testing.T) {
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
	ch, err := s.CreateChannel(ctx, domain.Channel{BroadcasterID: "1", Login: "a", DisplayName: "A"})
	if err != nil {
		t.Fatal(err)
	}
	f := twitch.Flow{ID: "lock-flow", Version: 1, Enabled: true, MinViews: 10, WindowHours: 72, MinAgeMinutes: 60, MinDurationSec: 5, MaxDurationSec: 60}
	if err = s.SyncFlows(ctx, []twitch.Flow{f}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var asc, desc []domain.Clip
	for i := 0; i < 300; i++ {
		asc = append(asc, domain.Clip{ID: fmt.Sprintf("lock-clip-%04d", i), ChannelID: ch.ID, BroadcasterID: ch.BroadcasterID, BroadcasterName: ch.Login, Duration: 20, ViewCount: 900, CreatedAt: now.Add(-2 * time.Hour)})
	}
	for i := len(asc) - 1; i >= 0; i-- {
		desc = append(desc, asc[i])
	}
	if err = s.UpsertClips(ctx, asc); err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveFlow(ctx, f, asc); err != nil {
		t.Fatal(err)
	}

	const rounds = 40
	errs := make(chan error, 4*rounds)
	var wg sync.WaitGroup
	run := func(fn func() error) {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if e := fn(); e != nil {
				errs <- e
			}
		}
	}
	cfg := twitch.CollectorConfig{Flows: []twitch.Flow{f}}
	wg.Add(4)
	go run(func() error { return s.SyncFlows(ctx, []twitch.Flow{f}) })
	go run(func() error { return s.SyncExclusions(ctx, cfg) })
	go run(func() error { return s.ObserveFlow(ctx, f, asc) })
	go run(func() error { return s.ObserveFlow(ctx, f, desc) })
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	var before, after string
	if err = s.pool.QueryRow(ctx, `SELECT xmin::text FROM twitch_collector.consumers WHERE flow_id=$1 AND clip_id=$2`, f.ID, asc[0].ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncFlows(ctx, []twitch.Flow{f}); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncExclusions(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveFlow(ctx, f, asc); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT xmin::text FROM twitch_collector.consumers WHERE flow_id=$1 AND clip_id=$2`, f.ID, asc[0].ID).Scan(&after); err != nil || before != after {
		t.Fatal("unchanged consumer still rewritten", before, after, err)
	}
	old := f
	f.Version++
	f.MinViews = 1000
	if err = s.SyncFlows(ctx, []twitch.Flow{f}); err != nil {
		t.Fatal(err)
	}
	if err = s.ObserveFlow(ctx, old, desc); err == nil {
		t.Fatal("stale discovery changed consumers after settings revision")
	}
	f.ExcludedSourceIDs = []string{asc[0].ID}
	if err = s.SyncFlows(ctx, []twitch.Flow{f}); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncExclusions(ctx, twitch.CollectorConfig{Flows: []twitch.Flow{f}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncExclusions(ctx, cfg); err == nil {
		t.Fatal("stale exclusions accepted after settings revision")
	}
	var excluded bool
	if err = s.pool.QueryRow(ctx, `SELECT excluded FROM twitch_collector.consumers WHERE flow_id=$1 AND clip_id=$2`, f.ID, asc[0].ID).Scan(&excluded); err != nil || !excluded {
		t.Fatal("stale exclusions removed current operator decision", excluded, err)
	}
	// Changing monitoring counters is not a configuration edit and cannot
	// re-evaluate/rewrite all consumers every five seconds.
	if err = s.pool.QueryRow(ctx, `SELECT xmin::text FROM twitch_collector.flows WHERE id=$1`, f.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	f.FoundClips, f.ReadyOutputs, f.BlockReason = 500, 20, "calendar full"
	f.LastCheckedAt = &now
	if err = s.SyncFlows(ctx, []twitch.Flow{f}); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT xmin::text FROM twitch_collector.flows WHERE id=$1`, f.ID).Scan(&after); err != nil || before != after {
		t.Fatal("runtime counters changed stored flow configuration", before, after, err)
	}
}
