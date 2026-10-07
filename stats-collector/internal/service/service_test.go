package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"terex/proto/workflow"
	"terex/stats-collector/internal/store"
)

type fakeGateway struct {
	byAccount map[int64]*workflow.ChannelStatsResponse
	calls     []int64
	err       error
}

func (g *fakeGateway) ChannelStats(_ context.Context, q *workflow.ChannelStatsRequest) (*workflow.ChannelStatsResponse, error) {
	g.calls = append(g.calls, q.AccountID)
	if g.err != nil {
		return nil, g.err
	}
	return g.byAccount[q.AccountID], nil
}

func ok(channel string, units int) *workflow.ChannelStatsResponse {
	return &workflow.ChannelStatsResponse{Status: workflow.StatsOK, QuotaUnits: units,
		Channel: workflow.ChannelStats{ChannelID: channel, Title: channel, Views: 10, Videos: 1},
		Videos:  []workflow.VideoStats{{ID: channel + "-v", Title: "v", PublishedAt: time.Now(), Views: 5}}}
}

// Needs a disposable database (see store_test.go): it recreates yt_stats and public.accounts.
func TestPassHandlesAccountStatuses(t *testing.T) {
	dsn := os.Getenv("STATS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("STATS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = st.Exec(ctx, `DROP TABLE IF EXISTS public.accounts; TRUNCATE yt_stats.channels CASCADE;
		CREATE TABLE public.accounts(id bigserial PRIMARY KEY, platform text, name text, remote_account_id text, connection_state text, credential_ref jsonb);
		INSERT INTO public.accounts(platform,name,remote_account_id,connection_state,credential_ref) SELECT 'youtube','A'||g,'UC'||g,'connected','{"bucket":"credentials","object":"google/1.json"}' FROM generate_series(1,4) g`); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := func(gw *fakeGateway) *Service {
		return New(Config{Interval: time.Hour, MaxVideos: 5, FullResolutionD: 14}, st, gw, log)
	}

	// re-auth and a broken account must not hide the healthy ones; only a broken one fails the pass
	gw := &fakeGateway{byAccount: map[int64]*workflow.ChannelStatsResponse{
		1: ok("UC1", 3),
		2: {Status: workflow.StatsNeedsReauth, Error: "reconnect"},
		3: {Status: workflow.StatsError, Error: "boom"},
		4: ok("UC4", 3),
	}}
	run := store.Run{}
	err = svc(gw).collect(ctx, &run)
	if err == nil || run.Channels != 2 || run.Videos != 2 || run.Units != 6 || len(gw.calls) != 4 {
		t.Fatalf("err=%v run=%+v calls=%v", err, run, gw.calls)
	}

	// without a failing account the pass is clean (re-auth alone is not a collector failure)
	gw.byAccount[3] = ok("UC3", 3)
	if err = svc(gw).collect(ctx, &store.Run{}); err != nil {
		t.Fatalf("clean pass failed: %v", err)
	}

	// quota stops the pass at once: remaining accounts are not queried
	gw = &fakeGateway{byAccount: map[int64]*workflow.ChannelStatsResponse{1: {Status: workflow.StatsQuota, Error: "q"}}}
	if err = svc(gw).collect(ctx, &store.Run{}); !errors.Is(err, errQuota) || len(gw.calls) != 1 {
		t.Fatalf("quota: err=%v calls=%v", err, gw.calls)
	}

	// an unreachable gateway aborts as well
	gw = &fakeGateway{err: errors.New("unavailable")}
	if err = svc(gw).collect(ctx, &store.Run{}); err == nil || len(gw.calls) != 1 {
		t.Fatalf("gateway down: err=%v calls=%v", err, gw.calls)
	}
}
