package postgres

import (
	"context"
	"fmt"
	"reflect"
	"terex/proto/twitch"
	"testing"
	"time"
)

func TestGrowthMeasurementsCounterResetAndRankingBeforeLimit(t *testing.T) {
	s, account, _ := twitchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	f := flowFixture(t, s, account, false, now)
	observe := func(src twitch.Source, at time.Time) twitch.Source {
		t.Helper()
		v, e := s.ObserveTwitchSources(ctx, twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: []twitch.Source{src}}, at)
		if e != nil {
			t.Fatal(e)
		}
		return v[0].Source
	}
	src := flowSource("measured-growth", "123", 20, now)
	src.CreatedAt = now.Add(-3 * time.Hour)
	src.ObservedAt = now.Add(-time.Hour)
	src.ViewCount = 100
	first := observe(src, src.ObservedAt)
	if first.Growth == nil || first.Growth.State != "warming" {
		t.Fatal(first.Growth)
	}
	src.ObservedAt = now
	src.ViewCount = 220
	measured := observe(src, now)
	if measured.Growth == nil || measured.Growth.State != "measured" || measured.Growth.ViewsPerHour != 120 || measured.Growth.DeltaViews != 120 || measured.Growth.Anomalous {
		t.Fatal(measured.Growth)
	}
	_ = observe(src, now)
	var samples int
	if e := s.pool.QueryRow(ctx, `SELECT count(*)FROM twitch_view_samples WHERE clip_id=$1`, src.ClipID).Scan(&samples); e != nil || samples != 2 {
		t.Fatal("duplicate observations added samples", samples, e)
	}
	watch, err := s.TwitchObservationCandidates(ctx, f.ID, 200, now)
	if err != nil || len(watch) != 1 || watch[0].ClipID != src.ClipID {
		t.Fatal("known clips unavailable for measured refresh", watch, err)
	}
	// A fast lower-view candidate survives the SQL limit; stale measurements
	// fall back to views rather than retaining a permanent ranking advantage.
	high := flowSource("high-views", "123", 20, now)
	high.ViewCount = 1000000
	query := `WITH i(twitch_clip_id,request)AS(VALUES('measured-growth',$1::jsonb),('high-views',$2::jsonb)) SELECT twitch_clip_id FROM i ORDER BY ` + twitchGrowthOrder("i", "$3", "$4::timestamptz", "$5") + ` LIMIT 1`
	for _, tc := range []struct {
		mode string
		at   time.Time
		want string
	}{{"growth", now, "measured-growth"}, {"views", now, "high-views"}, {"growth", now.Add(8 * time.Hour), "high-views"}} {
		var winner string
		e := s.pool.QueryRow(ctx, query, mustJSON(twitch.ImportRequest{Source: measured}), mustJSON(twitch.ImportRequest{Source: high}), tc.mode, tc.at, twitch.GrowthStaleAfter(f.PollIntervalMinutes).Seconds()).Scan(&winner)
		if e != nil || winner != tc.want {
			t.Fatal(tc, winner, e)
		}
	}
	src.ObservedAt = now.Add(time.Minute)
	src.ViewCount = 200
	reset := observe(src, src.ObservedAt)
	if reset.Growth.State != "counter_reset" {
		t.Fatal(reset.Growth)
	}
	src.ObservedAt = now.Add(16 * time.Minute)
	src.ViewCount = 230
	recovered := observe(src, src.ObservedAt)
	if recovered.Growth.State != "measured" || recovered.Growth.ViewsPerHour != 120 || recovered.Growth.DeltaViews != 30 {
		t.Fatal(recovered.Growth)
	}
	src.ObservedAt = now.Add(17 * time.Minute)
	src.ViewCount = 300
	_ = observe(src, src.ObservedAt)
	src.ObservedAt = now.Add(18 * time.Minute)
	src.ViewCount = 280
	withinBucketReset := observe(src, src.ObservedAt)
	if withinBucketReset.Growth.State != "counter_reset" {
		t.Fatal("counter correction between sampled points was missed", withinBucketReset.Growth)
	}
	// Foreign-language metadata must not consume the bounded candidate query.
	f.Language = "ru"
	f, err = s.SaveTwitchFlow(ctx, f, false, src.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	high.Language = "en"
	_, err = s.pool.Exec(ctx, `WITH inserted AS (INSERT INTO twitch_imports(id,idempotency_key,twitch_clip_id,request,state) SELECT 'foreign-'||n,'foreign-'||n,'foreign-'||n,jsonb_set($1::jsonb,'{source,clip_id}',to_jsonb('foreign-'||n)),'discovered' FROM generate_series(1,1001)n RETURNING id) INSERT INTO twitch_flow_sources(id,flow_id,import_id,config,flow_version) SELECT id,$2,id,$3,$4 FROM inserted`, mustJSON(twitch.ImportRequest{Source: high}), f.ID, mustJSON(f), f.Version)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := s.AllocateTwitchDownloads(ctx, twitch.AdmissionRequest{Limit: 1}, src.ObservedAt)
	if err != nil || len(admission.Grants) != 1 || admission.Grants[0].ClipID != src.ClipID {
		t.Fatal("eligible clip starved by foreign-language metadata", admission, err)
	}
	// Bulk observations keep the sequential broadcaster baseline: the final
	// clip sees the ten measurements before it in this very same transaction.
	batchAt := now.Add(20 * time.Minute)
	batch := make([]twitch.Source, 11)
	for n := range batch {
		batch[n] = flowSource(fmt.Sprintf("bulk-growth-%02d", n), "123", 20, batchAt)
		batch[n].CreatedAt = batchAt.Add(-3 * time.Hour)
		batch[n].ObservedAt = batchAt.Add(-time.Hour)
	}
	request := twitch.ObservationRequest{FlowID: f.ID, FlowVersion: f.Version, Sources: batch}
	if _, err = s.ObserveTwitchSources(ctx, request, batchAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for n := range batch {
		batch[n].ObservedAt = batchAt
		batch[n].ViewCount += 10
	}
	batch[10].ViewCount += 140
	measuredBatch, err := s.ObserveTwitchSources(ctx, request, batchAt)
	if err != nil || len(measuredBatch) != len(batch) || !measuredBatch[10].Source.Growth.Anomalous {
		t.Fatal("bulk growth lost its within-batch baseline", measuredBatch, err)
	}
	versions := func() []string {
		t.Helper()
		var out []string
		if err := s.pool.QueryRow(ctx, `SELECT array_agg(fs.xmin::text||':'||i.xmin::text ORDER BY i.twitch_clip_id) FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id WHERE fs.flow_id=$1 AND i.twitch_clip_id LIKE 'bulk-growth-%'`, f.ID).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := versions()
	if _, err = s.ObserveTwitchSources(ctx, request, batchAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if after := versions(); !reflect.DeepEqual(before, after) {
		t.Fatal("replayed metadata rewrote unchanged imports/flow configuration", before, after)
	}
}
