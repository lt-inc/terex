package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"sort"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

func flowPublicationAllowed(ctx context.Context, tx pgx.Tx, pid int64) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id JOIN accounts a ON a.id=p.account_id JOIN twitch_flows f ON f.id=o.flow_id WHERE p.id=$1 AND ((o.youtube_channel_id<>'' AND COALESCE(a.remote_account_id,'')<>o.youtube_channel_id) OR(p.started_at IS NULL AND (f.archived OR NOT f.enabled OR(p.auto_flow_id IS NOT NULL AND(NOT f.auto_publish OR p.auto_hold))))))`, pid).Scan(&allowed)
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
	if err := tx.QueryRow(ctx, `SELECT timezone,daily_shorts,daily_compilations FROM twitch_channel_limits WHERE youtube_channel_id=$1`, f.YouTubeChannelID).Scan(&timezone, &shorts, &comp); err != nil {
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
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM publications p JOIN accounts a ON a.id=p.account_id JOIN clips c ON c.clip_id=p.clip_id JOIN jobs j ON j.job_id=c.job_id LEFT JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE COALESCE(NULLIF(o.youtube_channel_id,''),a.remote_account_id)=$1 AND j.output_format=$2 AND p.id<>$3 AND p.status<>'cancelled' AND(p.publish_at AT TIME ZONE $4)::date=$5::date`, f.YouTubeChannelID, format, excludePublication, timezone, date.Format("2006-01-02")).Scan(&count); err != nil {
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
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publications p JOIN accounts a ON a.id=p.account_id JOIN clips c ON c.clip_id=p.clip_id JOIN jobs j ON j.job_id=c.job_id LEFT JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE COALESCE(NULLIF(o.youtube_channel_id,''),a.remote_account_id)=$1 AND j.output_format=$2 AND p.id<>$3 AND p.status<>'cancelled' AND date_trunc('minute',p.publish_at)=$4)`, f.YouTubeChannelID, format, excludePublication, at).Scan(&occupied); err != nil {
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if err = expirePreparationTx(ctx, tx, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM twitch_publication_slots s USING publications p WHERE p.id=s.publication_id AND(p.status IN('cancelled','on_hold','failed','blocked') OR p.auto_flow_id IS NULL OR EXISTS(SELECT 1 FROM twitch_channel_limits l WHERE l.youtube_channel_id=s.youtube_channel_id AND(s.local_date<>(p.publish_at AT TIME ZONE l.timezone)::date OR s.slot<>to_char(p.publish_at AT TIME ZONE l.timezone,'HH24:MI'))))`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT o.clip_id,o.config,o.format,COALESCE(p.id,0),r.revision_no,p.auto_hold FROM twitch_outputs o JOIN twitch_flows f ON f.id=o.flow_id JOIN clips c ON c.clip_id=o.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id JOIN accounts a ON a.id=o.account_id LEFT JOIN publications p ON p.clip_id=o.clip_id AND p.account_id=o.account_id WHERE o.auto_eligible AND c.approval<>'rejected' AND r.render_state='ready' AND COALESCE(r.object,'')<>'' AND f.enabled AND f.auto_publish AND NOT f.archived AND a.connection_state='connected' AND a.remote_account_id=o.youtube_channel_id AND(p.id IS NULL OR(p.auto_flow_id=o.flow_id AND p.auto_hold AND p.policy_hold_reason='calendar_horizon' AND p.started_at IS NULL AND p.status='on_hold')) ORDER BY o.created_at,o.clip_id LIMIT 50`)
	if err != nil {
		return err
	}
	type candidate struct {
		Clip, Format string
		Config       []byte
		PID          int64
		Version      int
		Hold         *bool
	}
	cs := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.Clip, &c.Config, &c.Format, &c.PID, &c.Version, &c.Hold); err != nil {
			rows.Close()
			return err
		}
		cs = append(cs, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, c := range cs {
		var f twitch.Flow
		if err = json.Unmarshal(c.Config, &f); err != nil {
			return err
		}
		// Rendering snapshots pin media/destination, never freshness policy.
		current, e := currentFlowPolicy(ctx, tx, f.ID)
		if e != nil {
			return e
		}
		f.ScheduleHorizonHours = current.ScheduleHorizonHours
		f.MaxSourceAgeHours = current.MaxSourceAgeHours
		f.ShortsSlots = current.ShortsSlots
		f.CompilationSlots = current.CompilationSlots
		if e = storageClipAvailable(ctx, tx, c.Clip); e != nil {
			if errors.Is(e, domain.ErrConflict) {
				continue
			}
			return e
		}
		if err = tx.QueryRow(ctx, `SELECT timezone,daily_shorts,daily_compilations FROM twitch_channel_limits WHERE youtube_channel_id=$1`, f.YouTubeChannelID).Scan(&f.Timezone, &f.DailyShorts, &f.DailyCompilations); err != nil {
			return err
		}
		if err = requireTwitchSources(ctx, tx, c.Clip, 0); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				continue
			}
			return err
		}
		if err = requireFlowFiltersTx(ctx, tx, c.Clip, 0); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				continue
			}
			return err
		}
		if err = requireFlowFiltersTx(ctx, tx, c.Clip, 0); err != nil {
			if errors.Is(err, domain.ErrConflict) {
				continue
			}
			return err
		}
		var prep string
		var reservedAt time.Time
		e = tx.QueryRow(ctx, `SELECT id,slot_at FROM twitch_preparation_reservations WHERE clip_id=$1 AND state='active' AND expires_at>$2 ORDER BY slot_at LIMIT 1`, c.Clip, now).Scan(&prep, &reservedAt)
		if e != nil && e != pgx.ErrNoRows {
			return e
		}
		// Reserve → publication is a transfer, not a second occupied slot.
		if prep != "" {
			if _, e = tx.Exec(ctx, `UPDATE twitch_preparation_reservations SET state='consumed' WHERE id=$1`, prep); e != nil {
				return e
			}
		}
		at, slot, ok, e := findFlowSlot(ctx, tx, f, c.Format, c.PID, now)
		if prep != "" && reservedAt.After(now) {
			at = reservedAt
			slot = at.In(mustLocation(f.Timezone)).Format("15:04")
			ok = true
		}
		if e != nil {
			return e
		}
		if !ok {
			continue
		}
		mode, e := policyMode(ctx, tx)
		if e != nil {
			return e
		}
		if mode == "enforce" {
			v, e := sourcePolicyViolation(ctx, tx, c.Clip, 0, at, now)
			if e != nil {
				return e
			}
			if v != nil {
				if c.PID != 0 {
					v.PublicationID = c.PID
					v.Automatic = true
					if e = holdPolicyPublication(ctx, tx, *v, now); e != nil {
						return e
					}
				}
				if _, e = tx.Exec(ctx, `UPDATE twitch_outputs SET auto_eligible=FALSE WHERE clip_id=$1`, c.Clip); e != nil {
					return e
				}
				continue
			}
		}
		if c.PID == 0 {
			if _, err = tx.Exec(ctx, `UPDATE sub_job_targets t SET timezone=$2 FROM sub_jobs sj,clips c WHERE t.sub_job_id=sj.sub_job_id AND sj.job_id=c.job_id AND c.clip_id=$1`, c.Clip, f.Timezone); err != nil {
				return err
			}
			if err = approveRevisionTx(ctx, tx, c.Clip, c.Version, now, &at, f.ID); err != nil {
				return err
			}
			if err = tx.QueryRow(ctx, `SELECT id FROM publications WHERE clip_id=$1 AND account_id=$2`, c.Clip, f.AccountID).Scan(&c.PID); err != nil {
				return err
			}
		} else {
			if _, err = tx.Exec(ctx, `UPDATE publications SET publish_at=$2,status='scheduled',auto_hold=FALSE,policy_hold_reason='',freshness_override_revision_id=NULL,freshness_override_publish_at=NULL,version=version+1,error=NULL,next_attempt_at=$3 WHERE id=$1 AND started_at IS NULL AND auto_hold AND policy_hold_reason='calendar_horizon'`, c.PID, at, now); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `DELETE FROM twitch_publication_slots WHERE publication_id=$1`, c.PID); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_publication_slots(flow_id,local_date,format,slot,publication_id,youtube_channel_id)VALUES($1,$2::date,$3,$4,$5,$6)`, f.ID, at.In(mustLocation(f.Timezone)).Format("2006-01-02"), c.Format, slot, c.PID, f.YouTubeChannelID); err != nil {
			return err
		}
		if err = insertEvent(ctx, tx, c.PID, "", "auto_scheduled", "scheduled", map[string]any{"flow_id": f.ID, "slot": slot, "publish_at": at}); err != nil {
			return err
		}
	}
	if err = enforceTwitchCaps(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
	flows, e := s.ListTwitchFlows(ctx)
	if e != nil {
		return e
	}
	for _, f := range flows {
		if f.Enabled && f.CompilationsEnabled {
			out, err := s.AutoFlowCompilation(ctx, f.ID, now)
			if err != nil {
				slog.ErrorContext(ctx, "twitch compilation failed", "flow_id", f.ID, "error", err)
				continue
			}
			eligible, pending := 0, 0
			if out.Sources != nil {
				eligible = out.Sources.Eligible
				pending = out.Sources.NotReady
			}
			slog.InfoContext(ctx, "twitch compilation readiness", "flow_id", f.ID, "reason", out.Reason, "eligible", eligible, "ready_duration_sec", out.ReadyDurationSec, "minimum_duration_sec", out.MinimumDurationSec, "missing_duration_sec", out.MissingDurationSec, "selected_count", out.SelectedClips, "pending_count", pending)
		}
	}
	return s.scheduleFlowOutputs(ctx, now)
}
