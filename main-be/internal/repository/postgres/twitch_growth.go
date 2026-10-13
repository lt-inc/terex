package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"terex/proto/twitch"
)

type twitchGrowthSample struct {
	ObservedAt   time.Time `json:"observed_at"`
	ViewCount    int64     `json:"view_count"`
	CounterEpoch int64     `json:"counter_epoch"`
}
type twitchGrowthClip struct {
	ImportID, MediaState string
	MetadataAt           time.Time
	MetadataViews        int64
	Latest               *twitchGrowthSample
	Samples              []twitchGrowthSample
}
type twitchGrowthBatch struct {
	clips     map[string]*twitchGrowthClip
	baselines map[string]map[string]float64
	flow      twitch.Flow
	now       time.Time
}

// Preload history once per observation batch. The ranges are bounded per clip;
// old samples cannot turn a large catalog into a full-table read.
func loadTwitchGrowthBatch(ctx context.Context, tx pgx.Tx, f twitch.Flow, sources []twitch.Source, now time.Time) (*twitchGrowthBatch, error) {
	out := &twitchGrowthBatch{clips: map[string]*twitchGrowthClip{}, baselines: map[string]map[string]float64{}, flow: f, now: now}
	type wantedClip struct {
		ClipID  string    `json:"clip_id"`
		StartAt time.Time `json:"start_at"`
		EndAt   time.Time `json:"end_at"`
	}
	wanted := map[string]wantedClip{}
	broadcasters := []string{}
	for _, src := range sources {
		at := src.ObservedAt
		if at.IsZero() || at.After(now) {
			at = now
		}
		w, exists := wanted[src.ClipID]
		if !exists {
			w = wantedClip{src.ClipID, at, at}
		}
		if at.Before(w.StartAt) {
			w.StartAt = at
		}
		if at.After(w.EndAt) {
			w.EndAt = at
		}
		wanted[src.ClipID] = w
		if _, exists = out.baselines[src.BroadcasterID]; !exists {
			out.baselines[src.BroadcasterID] = map[string]float64{}
			broadcasters = append(broadcasters, src.BroadcasterID)
		}
	}
	if len(wanted) == 0 {
		return out, nil
	}
	windows := make([]wantedClip, 0, len(wanted))
	for _, w := range wanted {
		windows = append(windows, w)
	}
	rows, err := tx.Query(ctx, `WITH wanted AS(SELECT * FROM jsonb_to_recordset($1::jsonb) AS w(clip_id text,start_at timestamptz,end_at timestamptz)) SELECT w.clip_id,COALESCE(i.id,''),COALESCE(i.media_state,''),COALESCE((i.request->'source'->>'observed_at')::timestamptz,'epoch'),COALESCE((i.request->'source'->>'view_count')::bigint,0),latest.observed_at,COALESCE(latest.view_count,0),COALESCE(latest.counter_epoch,0),COALESCE((SELECT jsonb_agg(jsonb_build_object('observed_at',s.observed_at,'view_count',s.view_count,'counter_epoch',s.counter_epoch)) FROM twitch_view_samples s WHERE s.clip_id=w.clip_id AND s.observed_at BETWEEN w.start_at-interval '6 hours' AND w.end_at), '[]'::jsonb) FROM wanted w LEFT JOIN twitch_imports i ON i.twitch_clip_id=w.clip_id LEFT JOIN LATERAL(SELECT observed_at,view_count,counter_epoch FROM twitch_view_samples WHERE clip_id=w.clip_id ORDER BY observed_at DESC LIMIT 1) latest ON true`, mustJSON(windows))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var clipID string
		var latestAt *time.Time
		var latestViews, epoch int64
		var samples []byte
		c := &twitchGrowthClip{}
		if err = rows.Scan(&clipID, &c.ImportID, &c.MediaState, &c.MetadataAt, &c.MetadataViews, &latestAt, &latestViews, &epoch, &samples); err != nil {
			rows.Close()
			return nil, err
		}
		if latestAt != nil {
			c.Latest = &twitchGrowthSample{*latestAt, latestViews, epoch}
		}
		if err = json.Unmarshal(samples, &c.Samples); err != nil {
			rows.Close()
			return nil, err
		}
		out.clips[clipID] = c
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// One lookup for all broadcasters replaces a whole-catalog scan per clip.
	rows, err = tx.Query(ctx, `SELECT request->'source'->>'broadcaster_id',twitch_clip_id,(request->'source'->'growth'->>'views_per_hour')::float8 FROM twitch_imports WHERE request->'source'->>'broadcaster_id'=ANY($1::text[]) AND request->'source'->'growth'->>'state'='measured' AND (request->'source'->'growth'->>'observed_at')::timestamptz>=$2 AND (request->'source'->>'created_at')::timestamptz>=$3`, broadcasters, now.Add(-twitch.GrowthStaleAfter(f.PollIntervalMinutes)), now.Add(-time.Duration(f.WindowHours)*time.Hour))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var broadcaster, clipID string
		var rate float64
		if err = rows.Scan(&broadcaster, &clipID, &rate); err != nil {
			rows.Close()
			return nil, err
		}
		if !math.IsNaN(rate) && !math.IsInf(rate, 0) && rate >= 0 {
			out.baselines[broadcaster][clipID] = rate
		}
	}
	rows.Close()
	return out, rows.Err()
}

func (b *twitchGrowthBatch) metadataUpdated(src twitch.Source) {
	c := b.clips[src.ClipID]
	if src.ObservedAt.IsZero() || src.ObservedAt.Before(c.MetadataAt) {
		return
	}
	c.MetadataAt, c.MetadataViews = src.ObservedAt, int64(src.ViewCount)
	baseline := b.baselines[src.BroadcasterID]
	delete(baseline, src.ClipID)
	if g := src.Growth; g != nil && g.State == "measured" && !g.ObservedAt.Before(b.now.Add(-twitch.GrowthStaleAfter(b.flow.PollIntervalMinutes))) && !src.CreatedAt.Before(b.now.Add(-time.Duration(b.flow.WindowHours)*time.Hour)) {
		baseline[src.ClipID] = g.ViewsPerHour
	}
}

// Samples and scores are authored here, rather than trusting scores from clients.
// Queuing the writes lets Observe send one batch instead of round-tripping for
// every sample; Reserve still executes its single-source write synchronously.
func (b *twitchGrowthBatch) record(ctx context.Context, tx pgx.Tx, writes *pgx.Batch, src twitch.Source) (twitch.Source, error) {
	src.Growth = nil
	if src.ObservedAt.IsZero() || src.ViewCount < 0 {
		return src, nil
	}
	if src.ObservedAt.After(b.now) {
		src.ObservedAt = b.now
	}
	c := b.clips[src.ClipID]
	var lastAt time.Time
	var lastViews, epoch int64
	if c.Latest != nil {
		lastAt, lastViews, epoch = c.Latest.ObservedAt, c.Latest.ViewCount, c.Latest.CounterEpoch
	}
	lastCounterAt, lastCounterViews := lastAt, lastViews
	if c.MetadataAt.After(lastCounterAt) {
		lastCounterAt, lastCounterViews = c.MetadataAt, c.MetadataViews
	}
	reset := src.ObservedAt.After(lastCounterAt) && !lastCounterAt.IsZero() && int64(src.ViewCount) < lastCounterViews
	if reset {
		epoch++
	}
	if c.Latest == nil || reset || src.ObservedAt.Sub(lastAt) >= 5*time.Minute {
		const insert = `INSERT INTO twitch_view_samples(clip_id,observed_at,view_count,counter_epoch) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`
		if writes != nil {
			writes.Queue(insert, src.ClipID, src.ObservedAt, src.ViewCount, epoch)
		} else if _, err := tx.Exec(ctx, insert, src.ClipID, src.ObservedAt, src.ViewCount, epoch); err != nil {
			return src, err
		}
		exists := false
		for _, sample := range c.Samples {
			if sample.ObservedAt.Equal(src.ObservedAt) {
				exists = true
				break
			}
		}
		if c.Latest != nil && c.Latest.ObservedAt.Equal(src.ObservedAt) {
			exists = true
		}
		if !exists {
			sample := twitchGrowthSample{src.ObservedAt, int64(src.ViewCount), epoch}
			c.Samples = append(c.Samples, sample)
			if c.Latest == nil || src.ObservedAt.After(c.Latest.ObservedAt) {
				c.Latest = &sample
			}
		}
	}
	g := &twitch.ClipGrowth{State: "warming", ObservedAt: src.ObservedAt}
	src.Growth = g
	if reset {
		g.State = "counter_reset"
		return src, nil
	}
	var previous *twitchGrowthSample
	start, end, target := src.ObservedAt.Add(-6*time.Hour), src.ObservedAt.Add(-15*time.Minute), src.ObservedAt.Add(-time.Hour)
	for n := range c.Samples {
		sample := &c.Samples[n]
		if sample.CounterEpoch != epoch || sample.ObservedAt.Before(start) || sample.ObservedAt.After(end) {
			continue
		}
		distance := math.Abs(sample.ObservedAt.Sub(target).Seconds())
		if previous == nil || distance < math.Abs(previous.ObservedAt.Sub(target).Seconds()) || (distance == math.Abs(previous.ObservedAt.Sub(target).Seconds()) && sample.ObservedAt.After(previous.ObservedAt)) {
			previous = sample
		}
	}
	if previous == nil || int64(src.ViewCount) < previous.ViewCount {
		return src, nil
	}
	g.State = "measured"
	g.DeltaViews = src.ViewCount - int(previous.ViewCount)
	g.IntervalSec = src.ObservedAt.Sub(previous.ObservedAt).Seconds()
	g.ViewsPerHour = float64(g.DeltaViews) * 3600 / g.IntervalSec
	// The badge is contextual evidence, never an admission filter or rank boost.
	if g.DeltaViews >= 25 && g.ViewsPerHour >= 75 {
		baseline := []float64{}
		for clipID, rate := range b.baselines[src.BroadcasterID] {
			if clipID != src.ClipID {
				baseline = append(baseline, rate)
			}
		}
		if len(baseline) >= 10 {
			sort.Float64s(baseline)
			median := baseline[len(baseline)/2]
			if len(baseline)%2 == 0 {
				median = (baseline[len(baseline)/2-1] + median) / 2
			}
			g.Anomalous = g.ViewsPerHour >= 3*median
		}
	}
	return src, nil
}
func recordTwitchGrowthTx(ctx context.Context, tx pgx.Tx, f twitch.Flow, src twitch.Source, now time.Time) (twitch.Source, error) {
	b, err := loadTwitchGrowthBatch(ctx, tx, f, []twitch.Source{src}, now)
	if err != nil {
		return src, err
	}
	return b.record(ctx, tx, nil, src)
}

// Mode and observation freshness participate before LIMIT in every SQL reader.
func twitchGrowthOrder(alias, mode, now, staleSeconds string) string {
	g := alias + `.request->'source'->'growth'`
	valid := fmt.Sprintf(`(%s='growth' AND %s->>'state'='measured' AND (%s->>'observed_at')::timestamptz BETWEEN %s-make_interval(secs=>%s) AND %s+interval '1 minute')`, mode, g, g, now, staleSeconds, now)
	return fmt.Sprintf(`CASE WHEN %s THEN 0 ELSE 1 END,CASE WHEN %s THEN COALESCE((%s->>'views_per_hour')::float8,0) ELSE 0 END DESC,COALESCE((%s.request->'source'->>'view_count')::bigint,0) DESC,(%s.request->'source'->>'created_at')::timestamptz DESC,%s.twitch_clip_id`, valid, valid, g, alias, alias, alias)
}

func (s *Store) TwitchObservationCandidates(ctx context.Context, id string, limit int, now time.Time) ([]twitch.Source, error) {
	f, err := s.GetTwitchFlow(ctx, id)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `SELECT i.request FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id WHERE fs.flow_id=$1 AND NOT fs.excluded AND NOT i.rejected AND i.media_state<>'expired' AND (i.request->'source'->>'created_at')::timestamptz>=$2 AND($4='' OR (i.request->'source'->>'language'=$4 AND i.request->'source'->>'language_source'='clip')) AND(($5='category' AND i.request->'source'->>'game_id'=$6) OR($5<>'category' AND(i.request->'source'->>'broadcaster_id'=ANY($7::text[]) OR $8))) AND(($9 AND NOT EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=$11 AND u.format='short' AND u.media_id=i.media_id)) OR($10 AND NOT EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=$11 AND u.format='compilation' AND u.media_id=i.media_id))) ORDER BY COALESCE((i.request->'source'->>'observed_at')::timestamptz,'epoch'),i.twitch_clip_id LIMIT $3`, id, now.Add(-time.Duration(min(f.WindowHours, f.MaxSourceAgeHours))*time.Hour), limit, f.Language, f.SourceMode, f.CategoryID, f.BroadcasterIDs, f.LegacyLength && len(f.BroadcasterIDs) == 0, f.ShortsEnabled, f.CompilationsEnabled, flowDestinationKey(f))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []twitch.Source{}
	for rows.Next() {
		var b []byte
		var r twitch.ImportRequest
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		if sourceMatches(f, r.Source) {
			out = append(out, r.Source)
		}
	}
	return out, rows.Err()
}

func (s *Store) CleanupTwitchGrowth(ctx context.Context, now time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM twitch_view_samples WHERE observed_at<$1`, now.Add(-7*24*time.Hour))
	return err
}
