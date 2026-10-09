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

// Samples and scores are authored here, rather than trusting scores from clients.
func recordTwitchGrowthTx(ctx context.Context, tx pgx.Tx, f twitch.Flow, src twitch.Source, now time.Time) (twitch.Source, error) {
	src.Growth = nil
	if src.ObservedAt.IsZero() || src.ViewCount < 0 {
		return src, nil
	}
	if src.ObservedAt.After(now) {
		src.ObservedAt = now
	}
	var lastAt time.Time
	var lastViews, epoch int64
	err := tx.QueryRow(ctx, `SELECT observed_at,view_count,counter_epoch FROM twitch_view_samples WHERE clip_id=$1 ORDER BY observed_at DESC LIMIT 1`, src.ClipID).Scan(&lastAt, &lastViews, &epoch)
	if err != nil && err != pgx.ErrNoRows {
		return src, err
	}
	lastCounterAt, lastCounterViews := lastAt, lastViews
	var metadataAt time.Time
	var metadataViews int64
	metaErr := tx.QueryRow(ctx, `SELECT COALESCE((request->'source'->>'observed_at')::timestamptz,'epoch'),COALESCE((request->'source'->>'view_count')::bigint,0) FROM twitch_imports WHERE twitch_clip_id=$1`, src.ClipID).Scan(&metadataAt, &metadataViews)
	if metaErr != nil && metaErr != pgx.ErrNoRows {
		return src, metaErr
	}
	if metadataAt.After(lastCounterAt) {
		lastCounterAt, lastCounterViews = metadataAt, metadataViews
	}
	reset := src.ObservedAt.After(lastCounterAt) && !lastCounterAt.IsZero() && int64(src.ViewCount) < lastCounterViews
	if reset {
		epoch++
	}
	if err == pgx.ErrNoRows || reset || src.ObservedAt.Sub(lastAt) >= 5*time.Minute {
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_view_samples(clip_id,observed_at,view_count,counter_epoch) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, src.ClipID, src.ObservedAt, src.ViewCount, epoch); err != nil {
			return src, err
		}
	}
	g := &twitch.ClipGrowth{State: "warming", ObservedAt: src.ObservedAt}
	src.Growth = g
	if reset {
		g.State = "counter_reset"
		return src, nil
	}
	var previousAt time.Time
	var previousViews int64
	err = tx.QueryRow(ctx, `SELECT observed_at,view_count FROM twitch_view_samples WHERE clip_id=$1 AND counter_epoch=$2 AND observed_at BETWEEN $3::timestamptz-interval '6 hours' AND $3::timestamptz-interval '15 minutes' ORDER BY abs(extract(epoch FROM observed_at-($3::timestamptz-interval '1 hour'))),observed_at DESC LIMIT 1`, src.ClipID, epoch, src.ObservedAt).Scan(&previousAt, &previousViews)
	if err == pgx.ErrNoRows {
		return src, nil
	}
	if err != nil {
		return src, err
	}
	if int64(src.ViewCount) < previousViews {
		return src, nil
	}
	g.State = "measured"
	g.DeltaViews = src.ViewCount - int(previousViews)
	g.IntervalSec = src.ObservedAt.Sub(previousAt).Seconds()
	g.ViewsPerHour = float64(g.DeltaViews) * 3600 / g.IntervalSec
	// The badge is contextual evidence, never an admission filter or rank boost.
	if g.DeltaViews >= 25 && g.ViewsPerHour >= 75 {
		rows, e := tx.Query(ctx, `SELECT (request->'source'->'growth'->>'views_per_hour')::float8 FROM twitch_imports WHERE twitch_clip_id<>$1 AND request->'source'->>'broadcaster_id'=$2 AND request->'source'->'growth'->>'state'='measured' AND (request->'source'->'growth'->>'observed_at')::timestamptz>=$3 AND (request->'source'->>'created_at')::timestamptz>=$4`, src.ClipID, src.BroadcasterID, now.Add(-twitch.GrowthStaleAfter(f.PollIntervalMinutes)), now.Add(-time.Duration(f.WindowHours)*time.Hour))
		if e != nil {
			return src, e
		}
		baseline := []float64{}
		for rows.Next() {
			var v float64
			if e = rows.Scan(&v); e != nil {
				rows.Close()
				return src, e
			}
			if !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 {
				baseline = append(baseline, v)
			}
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return src, e
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
	rows, err := s.pool.Query(ctx, `SELECT i.request FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id WHERE fs.flow_id=$1 AND NOT fs.excluded AND NOT i.rejected AND i.media_state<>'expired' AND (i.request->'source'->>'created_at')::timestamptz>=$2 AND($4='' OR (i.request->'source'->>'language'=$4 AND i.request->'source'->>'language_source'='clip')) AND(($5='category' AND i.request->'source'->>'game_id'=$6) OR($5<>'category' AND(i.request->'source'->>'broadcaster_id'=ANY($7::text[]) OR $8))) AND(($9 AND NOT EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=$11 AND u.format='short' AND u.media_id=i.media_id)) OR($10 AND NOT EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=$11 AND u.format='compilation' AND u.media_id=i.media_id))) ORDER BY COALESCE((i.request->'source'->>'observed_at')::timestamptz,'epoch'),i.twitch_clip_id LIMIT $3`, id, now.Add(-time.Duration(min(f.WindowHours, f.MaxSourceAgeHours))*time.Hour), limit, f.Language, f.SourceMode, f.CategoryID, f.BroadcasterIDs, f.LegacyLength && len(f.BroadcasterIDs) == 0, f.ShortsEnabled, f.CompilationsEnabled, f.YouTubeChannelID)
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
