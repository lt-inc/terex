package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"sort"
	"terex/proto/twitch"
	"time"
)

func flowPublicationAllowed(ctx context.Context, tx pgx.Tx, pid int64) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id JOIN accounts a ON a.id=p.account_id JOIN twitch_flows f ON f.id=o.flow_id WHERE p.id=$1 AND ((p.destination_key<>'' AND COALESCE(CASE WHEN a.platform='youtube' THEN a.remote_account_id ELSE a.platform||':'||a.remote_account_id END,'')<>p.destination_key) OR(p.started_at IS NULL AND NOT EXISTS(SELECT 1 FROM twitch_flow_destinations d WHERE d.flow_id=f.id AND d.destination_key=p.destination_key AND d.enabled)) OR(p.started_at IS NULL AND (f.archived OR NOT f.enabled OR(p.auto_flow_id IS NOT NULL AND(p.auto_hold OR NOT EXISTS(SELECT 1 FROM twitch_flow_destinations d WHERE d.flow_id=f.id AND d.destination_key=p.destination_key AND d.enabled AND COALESCE((d.config->>'auto_publish')::bool,FALSE))))))))`, pid).Scan(&allowed)
	return allowed, err
}
func findFlowSlot(ctx context.Context, tx pgx.Tx, f twitch.Flow, format string, excludePublication int64, now time.Time) (time.Time, string, bool, error) {
	mode, err := policyMode(ctx, tx)
	if err != nil {
		return time.Time{}, "", false, err
	}
	if mode == "enforce" {
		slots, e := calendarSlotsTx(ctx, tx, f, format, excludePublication, "", now)
		if e != nil {
			return time.Time{}, "", false, e
		}
		for _, s := range slots {
			if !s.Occupied && !s.Preparing {
				return s.At, s.Slot, true, nil
			}
		}
		return time.Time{}, "", false, nil
	}
	slots := append([]string{}, f.ShortsSlots...)
	if format == "compilation" {
		slots = append([]string{}, f.CompilationSlots...)
	}
	sort.Strings(slots)
	var timezone string
	var shorts, comp int
	if err := tx.QueryRow(ctx, `SELECT timezone,daily_shorts,daily_compilations FROM twitch_channel_limits WHERE youtube_channel_id=$1`, flowDestinationKey(f)).Scan(&timezone, &shorts, &comp); err != nil {
		return time.Time{}, "", false, err
	}
	loc, _ := time.LoadLocation(timezone)
	local := now.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	limit := shorts
	if format == "compilation" {
		limit = comp
	}
	for d := 0; d < 31; d++ {
		date := day.AddDate(0, 0, d)
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM publications p JOIN accounts a ON a.id=p.account_id JOIN clips c ON c.clip_id=p.clip_id JOIN jobs j ON j.job_id=c.job_id LEFT JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE COALESCE(NULLIF(p.destination_key,''),NULLIF(o.youtube_channel_id,''),a.remote_account_id)=$1 AND j.output_format=$2 AND p.id<>$3 AND p.status<>'cancelled' AND(p.publish_at AT TIME ZONE $4)::date=$5::date`, flowDestinationKey(f), format, excludePublication, timezone, date.Format("2006-01-02")).Scan(&count); err != nil {
			return time.Time{}, "", false, err
		}
		if count >= limit {
			continue
		}
		for _, slot := range slots {
			t, _ := time.Parse("15:04", slot)
			at := time.Date(date.Year(), date.Month(), date.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			if !at.After(now) {
				continue
			}
			var occupied bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publications p JOIN accounts a ON a.id=p.account_id JOIN clips c ON c.clip_id=p.clip_id JOIN jobs j ON j.job_id=c.job_id LEFT JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE COALESCE(NULLIF(p.destination_key,''),NULLIF(o.youtube_channel_id,''),a.remote_account_id)=$1 AND j.output_format=$2 AND p.id<>$3 AND p.status<>'cancelled' AND date_trunc('minute',p.publish_at)=$4)`, flowDestinationKey(f), format, excludePublication, at).Scan(&occupied); err != nil {
				return time.Time{}, "", false, err
			}
			if !occupied {
				return at, slot, true, nil
			}
		}
	}
	return time.Time{}, "", false, nil
}
func (s *Store) scheduleFlowOutputs(ctx context.Context, now time.Time) error {
	return s.scheduleDestinationOutputs(ctx, now)
}
func mustLocation(zone string) *time.Location {
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return time.UTC
	}
	return loc
}
func (s *Store) TickTwitchFlows(ctx context.Context, now time.Time) error {
	if err := s.autoApproveTwitchReady(ctx, now); err != nil {
		return err
	}
	flows, e := s.ListCollectorTwitchFlows(ctx)
	if e != nil {
		return e
	}
	for _, f := range flows {
		if f.Enabled && f.CompilationsEnabled {
			out, err := s.autoFlowCompilation(ctx, f.ID, now, true)
			if err != nil {
				slog.ErrorContext(ctx, "twitch compilation failed", "flow_id", f.ID, "error", err)
				continue
			}
			eligible := 0
			if out.Sources != nil {
				eligible = out.Sources.Eligible
			}
			slog.InfoContext(ctx, "twitch compilation readiness", "flow_id", f.ID, "reason", out.Reason, "eligible", eligible, "ready_duration_sec", out.ReadyDurationSec, "minimum_duration_sec", out.MinimumDurationSec, "missing_duration_sec", out.MissingDurationSec, "selected_count", out.SelectedClips, "pending_known", false)
		}
	}
	return s.scheduleFlowOutputs(ctx, now)
}
