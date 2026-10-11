package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
)

const occupiesCalendar = `p.status NOT IN('cancelled','on_hold','failed','blocked')`

func policyMode(ctx context.Context, tx pgx.Tx) (string, error) {
	var mode string
	err := tx.QueryRow(ctx, `SELECT mode FROM twitch_policy_settings WHERE id=1`).Scan(&mode)
	return mode, err
}

func currentFlowPolicy(ctx context.Context, tx pgx.Tx, id string) (twitch.Flow, error) {
	return scanFlow(tx.QueryRow(ctx, `SELECT `+flowCols+` FROM twitch_flows WHERE id=$1`, id))
}

func waitPreparation(code, message string, now time.Time) *domain.PreparationWaitError {
	return &domain.PreparationWaitError{PreparationWait: twitch.PreparationWait{Code: code, Message: message, NextCheckAt: now.Add(time.Minute)}}
}

// All admission, source attachment and GC callers hold lockTwitch. Reservations
// therefore protect a real slot, including across collector restarts/aliases.
func expirePreparationTx(ctx context.Context, tx pgx.Tx, now time.Time) error {
	if _, err := tx.Exec(ctx, `DELETE FROM twitch_preparation_sources ps USING twitch_preparation_reservations r,twitch_flows f,twitch_imports i,twitch_flow_sources fs WHERE ps.reservation_id=r.id AND r.flow_id=f.id AND ps.import_id=i.id AND fs.flow_id=f.id AND fs.import_id=i.id AND r.state='active' AND r.clip_id IS NULL AND(fs.excluded OR i.rejected OR i.state IN('failed','expired') OR
 (COALESCE(f.config->>'language','')<>'' AND(COALESCE(i.request->'source'->>'language','')<>f.config->>'language' OR COALESCE(i.request->'source'->>'language_source','')<>'clip')) OR
 (f.config->>'source_mode'='category' AND COALESCE(i.request->'source'->>'game_id','')<>COALESCE(f.config->>'category_id','')) OR
 (f.config->>'source_mode'='channels' AND COALESCE(jsonb_array_length(f.config->'broadcaster_ids'),0)>0 AND NOT(f.config->'broadcaster_ids' ? (i.request->'source'->>'broadcaster_id'))) OR
 COALESCE((i.request->'source'->>'view_count')::bigint,0)<COALESCE((f.config->>'min_views')::bigint,0) OR
 COALESCE((i.request->'source'->>'duration_sec')::float8,0)<COALESCE((f.config->>'min_duration_sec')::float8,5) OR
 COALESCE((i.request->'source'->>'duration_sec')::float8,0)>COALESCE((f.config->>'max_duration_sec')::float8,60) OR
 (i.request->'source'->>'created_at')::timestamptz<$1::timestamptz-make_interval(hours=>COALESCE((f.config->>'window_hours')::int,72)))`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE twitch_preparation_reservations r SET state='released',reason='source_filters_changed' WHERE state='active' AND clip_id IS NULL AND NOT EXISTS(SELECT 1 FROM twitch_preparation_sources ps WHERE ps.reservation_id=r.id)`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE twitch_preparation_reservations r SET state='released',reason='policy_changed_or_expired'
 FROM twitch_flows f JOIN twitch_channel_limits l ON l.youtube_channel_id=f.youtube_channel_id
 WHERE r.flow_id=f.id AND r.state='active' AND(r.expires_at<=$1 OR r.slot_at<=$1 OR f.archived OR NOT f.enabled
 OR r.slot_at>$1+make_interval(hours=>l.schedule_horizon_hours)
 OR EXISTS(SELECT 1 FROM twitch_preparation_sources ps JOIN twitch_imports i ON i.id=ps.import_id
 JOIN twitch_flow_sources fs ON fs.flow_id=ps.flow_id AND fs.import_id=ps.import_id
 WHERE ps.reservation_id=r.id AND(i.rejected OR fs.excluded OR i.media_state='expired'
 OR(i.request->'source'->>'created_at')::timestamptz+make_interval(hours=>COALESCE((f.config->>'max_source_age_hours')::int,72))<GREATEST($1,r.slot_at))))`, now)
	return err
}

type policySlot struct {
	At                  time.Time
	Slot                string
	Occupied, Preparing bool
}

func calendarSlotsTx(ctx context.Context, tx pgx.Tx, f twitch.Flow, format string, excludePID int64, excludePrep string, now time.Time) ([]policySlot, error) {
	var zone string
	var daily, horizon int
	err := tx.QueryRow(ctx, `SELECT timezone,CASE WHEN $2='compilation' THEN daily_compilations ELSE daily_shorts END,schedule_horizon_hours FROM twitch_channel_limits WHERE youtube_channel_id=$1`, f.YouTubeChannelID, format).Scan(&zone, &daily, &horizon)
	if err != nil {
		return nil, err
	}
	loc := mustLocation(zone)
	slots := append([]string{}, f.ShortsSlots...)
	if format == "compilation" {
		slots = append([]string{}, f.CompilationSlots...)
	}
	sort.Strings(slots)
	end := now.Add(time.Duration(horizon) * time.Hour)
	local := now.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	out := []policySlot{}
	for d := 0; d <= 8; d++ {
		date := day.AddDate(0, 0, d)
		if date.After(end) {
			break
		}
		var occupied, preparing int
		if err = tx.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM publications p JOIN accounts a ON a.id=p.account_id JOIN clips c ON c.clip_id=p.clip_id JOIN jobs j ON j.job_id=c.job_id LEFT JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE COALESCE(NULLIF(o.youtube_channel_id,''),a.remote_account_id)=$1 AND j.output_format=$2 AND p.id<>$3 AND `+occupiesCalendar+` AND(p.publish_at AT TIME ZONE $4)::date=$5::date),
  (SELECT count(*) FROM twitch_preparation_reservations r WHERE r.youtube_channel_id=$1 AND r.format=$2 AND r.id<>$6 AND r.state='active' AND r.expires_at>$7 AND(r.slot_at AT TIME ZONE $4)::date=$5::date AND NOT EXISTS(SELECT 1 FROM publications p WHERE p.clip_id=r.clip_id AND `+occupiesCalendar+`))`, f.YouTubeChannelID, format, excludePID, zone, date.Format("2006-01-02"), excludePrep, now).Scan(&occupied, &preparing); err != nil {
			return nil, err
		}
		freeForDay := max(0, daily-occupied-preparing)
		for _, slot := range slots {
			clock, e := time.Parse("15:04", slot)
			if e != nil {
				continue
			}
			at := time.Date(date.Year(), date.Month(), date.Day(), clock.Hour(), clock.Minute(), 0, 0, loc)
			if !at.After(now) || at.After(end) {
				continue
			}
			v := policySlot{At: at, Slot: slot}
			if err = tx.QueryRow(ctx, `SELECT
   EXISTS(SELECT 1 FROM publications p JOIN accounts a ON a.id=p.account_id JOIN clips c ON c.clip_id=p.clip_id JOIN jobs j ON j.job_id=c.job_id LEFT JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE COALESCE(NULLIF(o.youtube_channel_id,''),a.remote_account_id)=$1 AND j.output_format=$2 AND p.id<>$3 AND `+occupiesCalendar+` AND date_trunc('minute',p.publish_at)=$4),
   EXISTS(SELECT 1 FROM twitch_preparation_reservations r WHERE r.youtube_channel_id=$1 AND r.format=$2 AND r.id<>$5 AND r.state='active' AND r.expires_at>$6 AND r.slot_at=$4 AND NOT EXISTS(SELECT 1 FROM publications p WHERE p.clip_id=r.clip_id AND `+occupiesCalendar+`))`, f.YouTubeChannelID, format, excludePID, at, excludePrep, now).Scan(&v.Occupied, &v.Preparing); err != nil {
				return nil, err
			}
			if !v.Occupied && !v.Preparing {
				if freeForDay <= 0 {
					continue
				} else {
					freeForDay--
				}
			}
			out = append(out, v)
		}
	}
	return out, nil
}

func preparationSourceFresh(f twitch.Flow, src twitch.Source, at, now time.Time) bool {
	if src.CreatedAt.IsZero() {
		return false
	}
	if at.Before(now) {
		at = now
	}
	age := f.MaxSourceAgeHours
	if age == 0 {
		age = 72
	}
	return !at.After(src.CreatedAt.Add(time.Duration(age) * time.Hour))
}

func reservePreparationFormat(ctx context.Context, tx pgx.Tx, f twitch.Flow, id, format string, src twitch.Source, now time.Time) (string, bool, error) {
	if err := twitchResetAllowed(ctx, tx); err != nil {
		return "", false, err
	}
	// The actual channel/format source identity is shared by OAuth aliases.
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_source_uses u JOIN twitch_imports i ON i.media_id=u.media_id WHERE i.id=$1 AND u.youtube_channel_id=$2 AND u.format=$3) OR EXISTS(SELECT 1 FROM twitch_preparation_sources ps JOIN twitch_preparation_reservations r ON r.id=ps.reservation_id WHERE ps.import_id=$1 AND r.youtube_channel_id=$2 AND r.format=$3 AND r.state='active' AND r.expires_at>$4)`, id, f.YouTubeChannelID, format, now).Scan(&duplicate); err != nil {
		return "", false, err
	}
	if duplicate {
		return "", true, nil
	}
	if err := storagePreparationAvailable(ctx, tx, now); err != nil {
		return "", false, err
	}
	if format == "compilation" {
		rows, err := tx.Query(ctx, `SELECT r.id,r.slot_at,COALESCE(sum(ps.duration_sec),0),count(ps.import_id) FROM twitch_preparation_reservations r LEFT JOIN twitch_preparation_sources ps ON ps.reservation_id=r.id WHERE r.flow_id=$1 AND r.format='compilation' AND r.state='active' AND r.clip_id IS NULL AND r.expires_at>$2 GROUP BY r.id,r.slot_at ORDER BY r.slot_at`, f.ID, now)
		if err != nil {
			return "", false, err
		}
		type batch struct {
			ID       string
			At       time.Time
			Duration float64
			Count    int
		}
		batches := []batch{}
		for rows.Next() {
			var b batch
			if err = rows.Scan(&b.ID, &b.At, &b.Duration, &b.Count); err != nil {
				rows.Close()
				return "", false, err
			}
			batches = append(batches, b)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return "", false, err
		}
		for _, b := range batches {
			if b.Count >= f.CompilationMaxClips || b.Duration+float64(b.Count)+math.Ceil(src.DurationSec*30-1e-7)/30 > f.CompilationMaxDurationSec+0.05 || !preparationSourceFresh(f, src, b.At, now) {
				continue
			}
			_, err = tx.Exec(ctx, `INSERT INTO twitch_preparation_sources(reservation_id,import_id,flow_id,duration_sec)VALUES($1,$2,$3,$4)ON CONFLICT DO NOTHING`, b.ID, id, f.ID, math.Ceil(src.DurationSec*30-1e-7)/30)
			return b.ID, err == nil, err
		}
		// Accumulate one nearest release at a time; a full pending batch waits
		// for its files rather than filling another distant calendar day.
		if len(batches) > 0 {
			return "", false, nil
		}
	}
	slots, err := calendarSlotsTx(ctx, tx, f, format, 0, "", now)
	if err != nil {
		return "", false, err
	}
	for _, slot := range slots {
		if slot.Occupied || slot.Preparing || !preparationSourceFresh(f, src, slot.At, now) {
			continue
		}
		if format == "compilation" && math.Ceil(src.DurationSec*30-1e-7)/30 > f.CompilationMaxDurationSec+0.05 {
			continue
		}
		rid := uuid.NewString()
		expiry := slot.At.Add(15 * time.Minute)
		if deadline := src.CreatedAt.Add(time.Duration(f.MaxSourceAgeHours) * time.Hour); deadline.Before(expiry) {
			expiry = deadline
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_preparation_reservations(id,flow_id,youtube_channel_id,format,slot_at,expires_at,created_at)VALUES($1,$2,$3,$4,$5,$6,$7)`, rid, f.ID, f.YouTubeChannelID, format, slot.At, expiry, now); err != nil {
			return "", false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO twitch_preparation_sources(reservation_id,import_id,flow_id,duration_sec)VALUES($1,$2,$3,$4)`, rid, id, f.ID, math.Ceil(src.DurationSec*30-1e-7)/30)
		return rid, err == nil, err
	}
	return "", false, nil
}

func admitPreparationTx(ctx context.Context, tx pgx.Tx, f twitch.Flow, id string, src twitch.Source, now time.Time) error {
	mode, err := policyMode(ctx, tx)
	if err != nil {
		return err
	}
	if mode != "enforce" {
		return nil
	}
	if err = expirePreparationTx(ctx, tx, now); err != nil {
		return err
	}
	if !f.Enabled || f.Archived {
		return waitPreparation("flow_paused", "Поток остановлен или архивирован", now)
	}
	if !preparationSourceFresh(f, src, now, now) {
		return waitPreparation("source_stale", "Клип уже старше максимального возраста публикации", now)
	}
	admitted := false
	if f.ShortsEnabled {
		_, ok, e := reservePreparationFormat(ctx, tx, f, id, "short", src, now)
		if e != nil {
			return e
		}
		admitted = admitted || ok
	}
	if f.CompilationsEnabled {
		_, ok, e := reservePreparationFormat(ctx, tx, f, id, "compilation", src, now)
		if e != nil {
			return e
		}
		admitted = admitted || ok
	}
	if admitted {
		return nil
	}
	code, message := "calendar_full", "Календарь ближайших выпусков заполнен; новые скачивания приостановлены"
	for _, format := range []string{"short", "compilation"} {
		if (format == "short" && !f.ShortsEnabled) || (format == "compilation" && !f.CompilationsEnabled) {
			continue
		}
		slots, e := calendarSlotsTx(ctx, tx, f, format, 0, "", now)
		if e != nil {
			return e
		}
		for _, slot := range slots {
			if !slot.Occupied && !slot.Preparing {
				code = "stale_for_slot"
				message = "Клип устареет до ближайшего свободного места"
				break
			}
		}
	}
	return waitPreparation(code, message, now)
}

func admitImportConsumersTx(ctx context.Context, tx pgx.Tx, id, owner string, continuing bool, now time.Time) error {
	mode, err := policyMode(ctx, tx)
	if err != nil || mode != "enforce" {
		return err
	}
	out, err := importByID(ctx, tx, id)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT fs.flow_id FROM twitch_flow_sources fs JOIN twitch_flows f ON f.id=fs.flow_id WHERE fs.import_id=$1 AND NOT fs.excluded AND f.enabled AND NOT f.archived AND($3 OR fs.flow_id=$2) ORDER BY(fs.flow_id=$2)DESC,f.created_at`, id, owner, continuing)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var fid string
		if err = rows.Scan(&fid); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, fid)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var waiting error = waitPreparation("preparation_wait", "Нет действующей потребности в подготовке исходника", now)
	for _, fid := range ids {
		f, e := currentFlowPolicy(ctx, tx, fid)
		if e != nil {
			return e
		}
		if confirmedFlow(ctx, tx, f) != nil {
			continue
		}
		if !preparationSourceFresh(f, out.Source, now, now) {
			waiting = waitPreparation("source_stale", "Клип устарел для всех доступных потребителей", now)
			continue
		}
		if !sourceEligible(f, out.Source, now) {
			continue
		}
		e = admitPreparationTx(ctx, tx, f, id, out.Source, now)
		if e == nil {
			return nil
		}
		if !errors.Is(e, domain.ErrConflict) {
			return e
		}
		waiting = e
	}
	return waiting
}

func reserveOutputPreparationTx(ctx context.Context, tx pgx.Tx, f twitch.Flow, clip, format string, ids []string, now time.Time) (time.Time, error) {
	if err := twitchResetAllowed(ctx, tx); err != nil {
		return time.Time{}, err
	}
	mode, err := policyMode(ctx, tx)
	if err != nil {
		return time.Time{}, err
	}
	if mode != "enforce" {
		return time.Time{}, nil
	}
	if err = storagePreparationAvailable(ctx, tx, now); err != nil {
		return time.Time{}, err
	}
	if err = expirePreparationTx(ctx, tx, now); err != nil {
		return time.Time{}, err
	}
	var reservation string
	var at time.Time
	// Prefer the durable batch reserved before downloading.
	err = tx.QueryRow(ctx, `SELECT r.id,r.slot_at FROM twitch_preparation_reservations r WHERE r.flow_id=$1 AND r.format=$2 AND r.state='active' AND r.expires_at>$3 AND(r.clip_id IS NULL OR r.clip_id=$4) AND($2='compilation' OR NOT EXISTS(SELECT 1 FROM unnest($5::text[]) x WHERE NOT EXISTS(SELECT 1 FROM twitch_preparation_sources ps WHERE ps.reservation_id=r.id AND ps.import_id=x))) ORDER BY r.slot_at LIMIT 1`, f.ID, format, now, clip, ids).Scan(&reservation, &at)
	if err != nil && err != pgx.ErrNoRows {
		return at, err
	}
	var oldest time.Time
	if err = tx.QueryRow(ctx, `SELECT min((request->'source'->>'created_at')::timestamptz) FROM twitch_imports WHERE id=ANY($1::text[])`, ids).Scan(&oldest); err != nil {
		return at, err
	}
	if reservation == "" {
		slots, e := calendarSlotsTx(ctx, tx, f, format, 0, "", now)
		if e != nil {
			return at, e
		}
		for _, slot := range slots {
			if !slot.Occupied && !slot.Preparing && preparationSourceFresh(f, twitch.Source{CreatedAt: oldest}, slot.At, now) {
				at = slot.At
				break
			}
		}
		if at.IsZero() {
			return at, waitPreparation("calendar_full", "Нет свободного места для подготовки актуального выпуска", now)
		}
		reservation = uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_preparation_reservations(id,flow_id,youtube_channel_id,format,slot_at,expires_at,created_at)VALUES($1,$2,$3,$4,$5,$6,$7)`, reservation, f.ID, f.YouTubeChannelID, format, at, at.Add(15*time.Minute), now); err != nil {
			return at, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_preparation_sources(reservation_id,import_id,flow_id,duration_sec) SELECT $1,id,$2,COALESCE((verified->>'duration_sec')::float8,(request->'source'->>'duration_sec')::float8) FROM twitch_imports WHERE id=ANY($3::text[])`, reservation, f.ID, ids); err != nil {
			return at, err
		}
	}
	if !preparationSourceFresh(f, twitch.Source{CreatedAt: oldest}, at, now) {
		return at, waitPreparation("stale_for_slot", "Исходник устареет до даты выпуска", now)
	}
	if format == "compilation" {
		// Transfer all selected ready sources into one slot atomically. Old
		// waiting batches do not prevent reuse of a shared ready file.
		if _, err = tx.Exec(ctx, `DELETE FROM twitch_preparation_sources WHERE reservation_id=$1`, reservation); err != nil {
			return at, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_preparation_sources(reservation_id,import_id,flow_id,duration_sec) SELECT $1,i.id,$2,COALESCE((m.verified->>'duration_sec')::float8,(i.request->'source'->>'duration_sec')::float8) FROM twitch_imports i LEFT JOIN twitch_media m ON m.id=i.media_id WHERE i.id=ANY($3::text[])`, reservation, f.ID, ids); err != nil {
			return at, err
		}
		if _, err = tx.Exec(ctx, `UPDATE twitch_preparation_reservations SET state='released',reason='reconciled_into_nearest_compilation' WHERE flow_id=$1 AND format='compilation' AND state='active' AND clip_id IS NULL AND id<>$2`, f.ID, reservation); err != nil {
			return at, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE twitch_preparation_reservations SET clip_id=NULLIF($2,'') WHERE id=$1`, reservation, clip)
	return at, err
}

func (s *Store) FlowCapacity(ctx context.Context, id string, now time.Time) (twitch.FlowCapacity, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return twitch.FlowCapacity{}, err
	}
	defer tx.Rollback(ctx)
	f, err := currentFlowPolicy(ctx, tx, id)
	if err != nil {
		return twitch.FlowCapacity{}, err
	}
	mode, err := policyMode(ctx, tx)
	if err != nil {
		return twitch.FlowCapacity{}, err
	}
	out := twitch.FlowCapacity{FlowID: f.ID, YouTubeChannelID: f.YouTubeChannelID, HorizonHours: f.ScheduleHorizonHours, HorizonEnd: now.Add(time.Duration(f.ScheduleHorizonHours) * time.Hour), Mode: mode, Formats: []twitch.FormatCapacity{}}
	var storageWait *domain.PreparationWaitError
	if mode == "enforce" {
		if e := storagePreparationAvailable(ctx, tx, now); e != nil {
			if !errors.As(e, &storageWait) {
				return out, e
			}
		}
	}
	if f.YouTubeChannelID == "" {
		return out, nil
	}
	for _, format := range []string{"short", "compilation"} {
		if (format == "short" && !f.ShortsEnabled) || (format == "compilation" && !f.CompilationsEnabled) {
			continue
		}
		slots, e := calendarSlotsTx(ctx, tx, f, format, 0, "", now)
		if e != nil {
			return out, e
		}
		cap := twitch.FormatCapacity{Format: format, NextCheckAt: now.Add(time.Minute)}
		for _, slot := range slots {
			cap.TotalSlots++
			if slot.Occupied {
				cap.OccupiedSlots++
			} else if slot.Preparing {
				cap.PreparingSlots++
			} else {
				cap.FreeSlots++
				if cap.NextSlotAt == nil {
					at := slot.At
					cap.NextSlotAt = &at
				}
			}
		}
		if err = tx.QueryRow(ctx, `SELECT count(ps.import_id),COALESCE(sum(ps.duration_sec),0) FROM twitch_preparation_reservations r JOIN twitch_preparation_sources ps ON ps.reservation_id=r.id WHERE r.flow_id=$1 AND r.format=$2 AND r.state='active' AND r.expires_at>$3`, id, format, now).Scan(&cap.SourceCount, &cap.SourceDurationSec); err != nil {
			return out, err
		}
		if format == "compilation" {
			cap.MissingClips = max(0, f.CompilationMinClips-cap.SourceCount)
			cap.MissingDurationSec = math.Max(0, f.CompilationMinDurationSec-cap.SourceDurationSec-float64(max(0, cap.SourceCount-1)))
		}
		if cap.FreeSlots == 0 {
			cap.Reason = "calendar_full"
		}
		if !f.Enabled || f.Archived {
			cap.Reason = "flow_paused"
		}
		if storageWait != nil {
			cap.Reason = storageWait.Code
			cap.FreeSlots = 0
			cap.NextSlotAt = nil
			cap.NextCheckAt = storageWait.NextCheckAt
		}
		out.Formats = append(out.Formats, cap)
	}
	return out, nil
}

func sourcePolicyViolation(ctx context.Context, tx pgx.Tx, clip string, rid int64, at, now time.Time) (*domain.TwitchPolicyViolation, error) {
	if v, err := flowFilterViolationTx(ctx, tx, clip, rid); err != nil || v != nil {
		if v != nil {
			v.PublishAt = at
		}
		return v, err
	}
	if rid == 0 {
		if err := tx.QueryRow(ctx, `SELECT latest_revision_id FROM clips WHERE clip_id=$1`, clip).Scan(&rid); err != nil {
			return nil, err
		}
	}
	var flow string
	err := tx.QueryRow(ctx, `SELECT flow_id FROM twitch_outputs WHERE clip_id=$1`, clip).Scan(&flow)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f, err := currentFlowPolicy(ctx, tx, flow)
	if err != nil {
		return nil, err
	}
	v := &domain.TwitchPolicyViolation{ClipID: clip, FlowID: flow, PublishAt: at}
	var oldest *time.Time
	err = tx.QueryRow(ctx, `SELECT min((i.request->'source'->>'created_at')::timestamptz) FROM twitch_imports i WHERE i.id IN(SELECT source_id FROM twitch_outputs WHERE clip_id=$1 AND source_id IS NOT NULL UNION SELECT source_id FROM twitch_compilation_items WHERE compilation_revision_id=$2 AND source_id IS NOT NULL UNION SELECT i2.id FROM twitch_compilation_items ci JOIN twitch_imports i2 ON i2.clip_id=ci.source_clip_id WHERE ci.compilation_revision_id=$2)`, clip, rid).Scan(&oldest)
	if err != nil {
		return nil, err
	}
	v.SourceCreatedAt = oldest
	if oldest == nil {
		v.Code = "source_timestamp_missing"
		v.Message = "Неизвестна дата создания Twitch-исходника; нужна проверка оператора"
		return v, nil
	}
	deadline := oldest.Add(time.Duration(f.MaxSourceAgeHours) * time.Hour)
	v.Deadline = &deadline
	effective := at
	if effective.Before(now) {
		effective = now
	}
	if effective.After(deadline) {
		v.Code = "source_stale"
		v.Message = fmt.Sprintf("Исходник устарел к дате выпуска: крайний срок %s", deadline.In(mustLocation(f.Timezone)).Format("02.01.2006 15:04"))
		return v, nil
	}
	if at.After(now.Add(time.Duration(f.ScheduleHorizonHours) * time.Hour)) {
		v.Code = "calendar_horizon"
		v.Message = fmt.Sprintf("Выпуск за пределами ближайших %d часов", f.ScheduleHorizonHours)
		return v, nil
	}
	return nil, nil
}

func setPublicationOverrideTx(ctx context.Context, tx pgx.Tx, id int64, override bool, now time.Time) error {
	if override {
		var state string
		if err := tx.QueryRow(ctx, `UPDATE publications SET freshness_override_revision_id=revision_id,freshness_override_publish_at=publish_at WHERE id=$1 AND started_at IS NULL AND auto_flow_id IS NULL RETURNING status`, id).Scan(&state); err != nil {
			return err
		}
		return insertEvent(ctx, tx, id, "", "freshness_override", state, map[string]any{"confirmed_at": now, "scope": "exact_revision_and_publish_at"})
	}
	_, err := tx.Exec(ctx, `UPDATE publications SET freshness_override_revision_id=NULL,freshness_override_publish_at=NULL WHERE id=$1 AND(freshness_override_revision_id IS DISTINCT FROM revision_id OR freshness_override_publish_at IS DISTINCT FROM publish_at)`, id)
	return err
}

func enforceUnstartedPolicyTx(ctx context.Context, tx pgx.Tx, pid int64, now time.Time) (bool, error) {
	mode, err := policyMode(ctx, tx)
	if err != nil {
		return false, err
	}
	if mode != "enforce" {
		var clip string
		var rid int64
		var started bool
		if err = tx.QueryRow(ctx, `SELECT clip_id,COALESCE(revision_id,0),started_at IS NOT NULL FROM publications WHERE id=$1`, pid).Scan(&clip, &rid, &started); err != nil {
			return false, err
		}
		if started {
			return true, nil
		}
		v, e := flowFilterViolationTx(ctx, tx, clip, rid)
		if e != nil {
			return false, e
		}
		if v == nil {
			return true, nil
		}
		v.PublicationID = pid
		return false, holdPolicyPublication(ctx, tx, *v, now)
	}
	v, err := publicationPolicyViolation(ctx, tx, pid, now, false)
	if err != nil {
		return false, err
	}
	if v == nil || v.Protected {
		return true, nil
	}
	if err = holdPolicyPublication(ctx, tx, *v, now); err != nil {
		return false, err
	}
	return false, nil
}

// A claim precedes media download. The first provider-session checkpoint is the
// last safe point to stop an expired clip without abandoning a remote upload.
func enforceFirstUploadPolicyTx(ctx context.Context, tx pgx.Tx, pid int64, attempt string, now time.Time) (bool, error) {
	mode, err := policyMode(ctx, tx)
	if err != nil {
		return false, err
	}
	var initiated bool
	if err = tx.QueryRow(ctx, `SELECT p.cancel_requested OR p.upload_id IS NOT NULL OR EXISTS(SELECT 1 FROM publication_attempts pa WHERE pa.publication_id=p.id AND(COALESCE(pa.checkpoint->>'session_uri','')<>'' OR COALESCE(pa.checkpoint->>'remote_id','')<>'' OR COALESCE((pa.checkpoint->>'final_request_started')::boolean,false))) FROM publications p WHERE p.id=$1`, pid).Scan(&initiated); err != nil {
		return false, err
	}
	if initiated {
		return true, nil
	}
	if mode != "enforce" {
		var clip string
		var rid int64
		if err = tx.QueryRow(ctx, `SELECT clip_id,COALESCE(revision_id,0)FROM publications WHERE id=$1`, pid).Scan(&clip, &rid); err != nil {
			return false, err
		}
		v, e := flowFilterViolationTx(ctx, tx, clip, rid)
		if e != nil {
			return false, e
		}
		if v == nil {
			return true, nil
		}
		v.PublicationID = pid
		if err = holdPolicyPublication(ctx, tx, *v, now); err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `UPDATE publications SET started_at=NULL WHERE id=$1 AND upload_id IS NULL`, pid)
		return false, err
	}
	v, err := publicationPolicyViolation(ctx, tx, pid, now, true)
	if err != nil {
		return false, err
	}
	if v == nil {
		return true, nil
	}
	// No provider upload/session has been durably accepted yet.
	if err = holdPolicyPublication(ctx, tx, *v, now); err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET started_at=NULL WHERE id=$1 AND upload_id IS NULL`, pid); err != nil {
		return false, err
	}
	return false, nil
}

func publicationPolicyViolation(ctx context.Context, tx pgx.Tx, pid int64, now time.Time, forceStarted bool) (*domain.TwitchPolicyViolation, error) {
	var clip string
	var rid int64
	var at time.Time
	var automatic, protected, override bool
	err := tx.QueryRow(ctx, `SELECT p.clip_id,COALESCE(p.revision_id,0),p.publish_at,p.auto_flow_id IS NOT NULL,
 p.started_at IS NOT NULL OR p.upload_id IS NOT NULL OR p.status='needs_attention' OR EXISTS(SELECT 1 FROM publication_attempts pa WHERE pa.publication_id=p.id AND(COALESCE(pa.checkpoint->>'session_uri','')<>'' OR COALESCE((pa.checkpoint->>'final_request_started')::boolean,false))),
 COALESCE(p.auto_flow_id IS NULL AND p.freshness_override_revision_id=p.revision_id AND p.freshness_override_publish_at=p.publish_at,false) FROM publications p WHERE p.id=$1`, pid).Scan(&clip, &rid, &at, &automatic, &protected, &override)
	if err != nil {
		return nil, err
	}
	if override {
		v, e := flowFilterViolationTx(ctx, tx, clip, rid)
		if v != nil {
			v.PublicationID = pid
			v.Automatic = automatic
			v.Protected = protected
			v.PublishAt = at
		}
		return v, e
	}
	v, err := sourcePolicyViolation(ctx, tx, clip, rid, at, now)
	if err != nil || v == nil {
		return v, err
	}
	v.PublicationID = pid
	v.Automatic = automatic
	v.Protected = protected
	if protected && !forceStarted {
		return v, nil
	}
	return v, nil
}

func holdPolicyPublication(ctx context.Context, tx pgx.Tx, v domain.TwitchPolicyViolation, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE publication_id=$1 AND status='active'`, v.PublicationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE publications SET status='on_hold',auto_hold=auto_flow_id IS NOT NULL,policy_hold_reason=$2,version=version+1,error=$3 WHERE id=$1`, v.PublicationID, v.Code, v.Message); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM twitch_publication_slots WHERE publication_id=$1`, v.PublicationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE twitch_preparation_reservations SET state='released',reason=$2 WHERE clip_id=$1 AND state='active'`, v.ClipID, v.Code); err != nil {
		return err
	}
	if v.Code != "calendar_horizon" {
		if _, err := tx.Exec(ctx, `UPDATE twitch_outputs SET auto_eligible=FALSE WHERE clip_id=$1`, v.ClipID); err != nil {
			return err
		}
	}
	return insertEvent(ctx, tx, v.PublicationID, "", "policy_hold", "on_hold", map[string]any{"code": v.Code, "message": v.Message, "checked_at": now})
}

func previewPolicyTx(ctx context.Context, tx pgx.Tx, now time.Time) (domain.TwitchPolicyPreview, error) {
	mode, err := policyMode(ctx, tx)
	out := domain.TwitchPolicyPreview{Mode: mode, Violations: []domain.TwitchPolicyViolation{}}
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT p.id FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE p.status NOT IN('published','cancelled') ORDER BY p.publish_at,p.id`)
	if err != nil {
		return out, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	for _, id := range ids {
		v, e := publicationPolicyViolation(ctx, tx, id, now, false)
		if e != nil {
			return out, e
		}
		if v != nil {
			out.Violations = append(out.Violations, *v)
		}
	}
	return out, nil
}
func (s *Store) PreviewTwitchPolicy(ctx context.Context, now time.Time) (domain.TwitchPolicyPreview, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.TwitchPolicyPreview{}, err
	}
	defer tx.Rollback(ctx)
	return previewPolicyTx(ctx, tx, now)
}
func (s *Store) ApplyTwitchPolicy(ctx context.Context, now time.Time) (domain.TwitchPolicyPreview, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.TwitchPolicyPreview{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return domain.TwitchPolicyPreview{}, err
	}
	out, err := previewPolicyTx(ctx, tx, now)
	if err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_policy_settings SET mode='enforce',applied_at=$1 WHERE id=1`, now); err != nil {
		return out, err
	}
	out.Mode = "enforce"
	for _, v := range out.Violations {
		if !v.Automatic || v.Protected {
			continue
		}
		var alreadyHeld bool
		if err = tx.QueryRow(ctx, `SELECT status='on_hold' AND policy_hold_reason=$2 FROM publications WHERE id=$1`, v.PublicationID, v.Code).Scan(&alreadyHeld); err != nil {
			return out, err
		}
		if alreadyHeld {
			continue
		}
		if err = holdPolicyPublication(ctx, tx, v, now); err != nil {
			return out, err
		}
		out.Held++
	}
	if err = expirePreparationTx(ctx, tx, now); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func holdFlowAll(ctx context.Context, tx pgx.Tx, id, code, message string) error {
	if _, err := tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE status='active' AND publication_id IN(SELECT p.id FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE o.flow_id=$1 AND p.started_at IS NULL AND p.status NOT IN('published','cancelled','needs_attention'))`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE publications p SET status='on_hold',auto_hold=auto_flow_id IS NOT NULL,policy_hold_reason=$2,version=version+1,error=$3 FROM twitch_outputs o WHERE o.clip_id=p.clip_id AND o.flow_id=$1 AND p.started_at IS NULL AND p.status NOT IN('published','cancelled','needs_attention')`, id, code, message); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM twitch_publication_slots s USING publications p WHERE p.id=s.publication_id AND p.status='on_hold'`)
	return err
}
func (s *Store) ListTwitchFlowsIncludingArchived(ctx context.Context) ([]twitch.Flow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+flowCols+` FROM twitch_flows ORDER BY archived,created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []twitch.Flow{}
	for rows.Next() {
		f, e := scanFlow(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Store) RestoreTwitchFlow(ctx context.Context, id string, now time.Time) (twitch.Flow, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return twitch.Flow{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return twitch.Flow{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE twitch_flows SET archived=FALSE,enabled=FALSE,auto_publish=FALSE,auto_enabled_at=NULL,version=version+1,updated_at=$2 WHERE id=$1 AND archived`, id, now)
	if err != nil {
		return twitch.Flow{}, err
	}
	if tag.RowsAffected() == 0 {
		return twitch.Flow{}, domain.ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return twitch.Flow{}, err
	}
	return s.GetTwitchFlow(ctx, id)
}

func (s *Store) AbortFlowImport(ctx context.Context, id string, generation int64, reason string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	out, err := importByID(ctx, tx, id)
	if err != nil {
		return err
	}
	if out.UploadGeneration != generation {
		return domain.ErrConflict
	}
	if out.MediaState == "ready" {
		return tx.Commit(ctx)
	}
	if err = abortFlowImportTx(ctx, tx, id, generation, reason, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func abortFlowImportTx(ctx context.Context, tx pgx.Tx, id string, generation int64, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE twitch_imports SET state='failed',media_state='failed',error=$2,upload_claim_until=NULL,updated_at=$3 WHERE id=$1 AND upload_generation=$4 AND media_state<>'ready'`, id, reason, now, generation); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM twitch_preparation_sources WHERE import_id=$1 AND reservation_id IN(SELECT id FROM twitch_preparation_reservations WHERE state='active')`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE twitch_download_grants SET state='released' WHERE import_id=$1 AND state='active'`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE twitch_preparation_reservations r SET state='released',reason='source_failed' WHERE r.state='active' AND NOT EXISTS(SELECT 1 FROM twitch_preparation_sources ps WHERE ps.reservation_id=r.id)`); err != nil {
		return err
	}
	return nil
}

// Validate inside the same transaction that assigns the exact revision/date.
func validatePublicationPolicyTx(ctx context.Context, tx pgx.Tx, pid int64, now time.Time) error {
	var clip string
	var rid int64
	if err := tx.QueryRow(ctx, `SELECT clip_id,COALESCE(revision_id,0)FROM publications WHERE id=$1`, pid).Scan(&clip, &rid); err != nil {
		return err
	}
	if err := requireFlowFiltersTx(ctx, tx, clip, rid); err != nil {
		return err
	}
	mode, err := policyMode(ctx, tx)
	if err != nil || mode != "enforce" {
		return err
	}
	v, err := publicationPolicyViolation(ctx, tx, pid, now, false)
	if err != nil || v == nil {
		return err
	}
	if v.Protected {
		return nil
	}
	return waitPreparation(v.Code, v.Message+"; измените дату или подтвердите ручное исключение для этой версии", now)
}

func renderPreparationAllowed(ctx context.Context, tx pgx.Tx, rid int64, now time.Time) (bool, error) {
	mode, err := policyMode(ctx, tx)
	if err != nil {
		return false, err
	}
	if mode != "enforce" {
		return true, nil
	}
	var clip, flow, format string
	err = tx.QueryRow(ctx, `SELECT o.clip_id,o.flow_id,o.format FROM twitch_outputs o JOIN clip_revisions r ON r.clip_id=o.clip_id WHERE r.id=$1`, rid).Scan(&clip, &flow, &format)
	if err == pgx.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if err = storageClipAvailable(ctx, tx, clip); err != nil {
		return false, err
	}
	f, err := currentFlowPolicy(ctx, tx, flow)
	if err != nil {
		return false, err
	}
	if !f.Enabled || f.Archived {
		return false, nil
	}
	rows, err := tx.Query(ctx, `SELECT source_id FROM twitch_outputs WHERE clip_id=$1 AND source_id IS NOT NULL UNION SELECT source_id FROM twitch_compilation_items WHERE compilation_revision_id=$2 AND source_id IS NOT NULL`, clip, rid)
	if err != nil {
		return false, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return false, nil
	}
	_, err = reserveOutputPreparationTx(ctx, tx, f, clip, format, ids, now)
	if errors.Is(err, domain.ErrConflict) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) enrichPublicationPolicy(ctx context.Context, p *domain.PublicationRecord) error {
	var flow string
	var created *time.Time
	var maxAge, horizon int
	var override bool
	err := s.pool.QueryRow(ctx, `SELECT f.id,COALESCE((f.config->>'max_source_age_hours')::int,72),l.schedule_horizon_hours,
 (SELECT min((i.request->'source'->>'created_at')::timestamptz) FROM twitch_imports i WHERE i.id=o.source_id OR i.id IN(SELECT source_id FROM twitch_compilation_items WHERE compilation_revision_id=p.revision_id) OR i.clip_id IN(SELECT source_clip_id FROM twitch_compilation_items WHERE compilation_revision_id=p.revision_id)),
 COALESCE(p.auto_flow_id IS NULL AND p.freshness_override_revision_id=p.revision_id AND p.freshness_override_publish_at=p.publish_at,false)
 FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id JOIN twitch_flows f ON f.id=o.flow_id JOIN twitch_channel_limits l ON l.youtube_channel_id=o.youtube_channel_id WHERE p.id=$1`, p.ID).Scan(&flow, &maxAge, &horizon, &created, &override)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	policy := &domain.PublicationTwitchPolicy{FlowID: flow, SourceCreatedAt: created, MaxSourceAgeHours: maxAge, HorizonHours: horizon, HorizonEnd: time.Now().UTC().Add(time.Duration(horizon) * time.Hour)}
	if created != nil {
		deadline := created.Add(time.Duration(maxAge) * time.Hour)
		policy.Deadline = &deadline
	}
	p.TwitchPolicy = policy
	p.FreshnessOverride = override
	return nil
}
