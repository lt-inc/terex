package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"math"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

func defaultFlow() twitch.Flow {
	s := twitch.DefaultSettings()
	return twitch.Flow{Name: "Новый поток", SourceMode: "channels", Version: 1, PollIntervalMinutes: s.PollIntervalMinutes, WindowHours: s.WindowHours, MinViews: s.MinViews, MinAgeMinutes: s.MinAgeMinutes, MinDurationSec: s.MinDurationSec, MaxDurationSec: s.MaxDurationSec, ShortsEnabled: true, CompilationsEnabled: true, CompilationMinDurationSec: 480, CompilationMaxDurationSec: 720, CompilationMinClips: 3, CompilationMaxClips: 60, CompilationHour: 21, BalanceByTime: true, Timezone: s.Timezone, DailyShorts: s.DailyShorts, DailyCompilations: s.DailyCompilations, ShortsSlots: []string{"09:00", "12:00", "15:00", "18:00", "21:00"}, CompilationSlots: []string{"21:30"}, BroadcasterIDs: []string{}}
}

const flowCols = `config,version,COALESCE(account_id,0),youtube_channel_id,enabled,auto_publish,auto_enabled_at,created_at,updated_at,work_started_at,last_checked_at,
 (SELECT count(*) FROM twitch_flow_sources fs WHERE fs.flow_id=twitch_flows.id),
 (SELECT count(*) FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id WHERE o.flow_id=twitch_flows.id AND r.render_state='ready'),
 (SELECT min(p.publish_at) FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id WHERE o.flow_id=twitch_flows.id AND p.started_at IS NULL AND p.status IN('scheduled','queued','retry_wait')),
 CASE WHEN account_id IS NULL THEN 'Выберите YouTube-канал' WHEN NOT EXISTS(SELECT 1 FROM accounts a WHERE a.id=twitch_flows.account_id AND a.connection_state='connected' AND COALESCE(a.remote_account_id,'')=twitch_flows.youtube_channel_id AND twitch_flows.youtube_channel_id<>'') THEN 'Подключите подтверждённый YouTube-канал; назначение потока закреплено' ELSE '' END,
 COALESCE((SELECT jsonb_agg(i.twitch_clip_id) FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id WHERE fs.flow_id=twitch_flows.id AND fs.excluded),'[]'::jsonb)`

func scanFlow(row pgx.Row) (twitch.Flow, error) {
	f := defaultFlow()
	var b []byte
	var version int
	var account int64
	var channel string
	var enabled, auto bool
	var since *time.Time
	var created, updated time.Time
	var work, checked, next *time.Time
	var found, ready int
	var blocked string
	var excluded []byte
	err := row.Scan(&b, &version, &account, &channel, &enabled, &auto, &since, &created, &updated, &work, &checked, &found, &ready, &next, &blocked, &excluded)
	if err != nil {
		return f, mapErr(err)
	}
	if err = json.Unmarshal(b, &f); err != nil {
		return f, err
	}
	f.Version = version
	f.AccountID = account
	f.YouTubeChannelID = channel
	f.Enabled = enabled
	f.AutoPublish = auto
	f.AutoEnabledAt = since
	f.CreatedAt = created
	f.UpdatedAt = updated
	f.WorkStartedAt = work
	f.LastCheckedAt = checked
	f.FoundClips = found
	f.ReadyOutputs = ready
	f.NextPublicationAt = next
	f.BlockReason = blocked
	if err = json.Unmarshal(excluded, &f.ExcludedSourceIDs); err != nil {
		return f, err
	}
	if f.LegacyLength {
		f.CompilationMinDurationSec = 0
	}
	return f, nil
}
func flowByID(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (twitch.Flow, error) {
	return scanFlow(q.QueryRow(ctx, `SELECT `+flowCols+` FROM twitch_flows WHERE id=$1 AND NOT archived`, id))
}
func (s *Store) GetTwitchFlow(ctx context.Context, id string) (twitch.Flow, error) {
	return flowByID(ctx, s.pool, id)
}
func (s *Store) ListTwitchFlows(ctx context.Context) ([]twitch.Flow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+flowCols+` FROM twitch_flows WHERE NOT archived ORDER BY created_at,id`)
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
func validateFlow(f twitch.Flow) error {
	if strings.TrimSpace(f.Name) == "" || len([]rune(f.Name)) > 100 {
		return fmt.Errorf("%w: задайте название потока до 100 символов", domain.ErrInvalid)
	}
	if f.SourceMode != "channels" && f.SourceMode != "category" {
		return fmt.Errorf("%w: выберите каналы или категорию", domain.ErrInvalid)
	}
	if f.SourceMode == "category" && f.CategoryID == "" {
		return fmt.Errorf("%w: выберите категорию Twitch", domain.ErrInvalid)
	}
	if f.Enabled && f.SourceMode == "channels" && len(f.BroadcasterIDs) == 0 && !f.LegacyLength {
		return fmt.Errorf("%w: добавьте каналы Twitch", domain.ErrInvalid)
	}
	for _, v := range f.BroadcasterIDs {
		if v == "" || strings.Trim(v, "0123456789") != "" {
			return fmt.Errorf("%w: invalid broadcaster id", domain.ErrInvalid)
		}
	}
	if len(f.Language) > 12 || len(f.CategoryID) > 32 || len(f.BroadcasterIDs) > 200 {
		return domain.ErrInvalid
	}
	if f.PollIntervalMinutes < 1 || f.PollIntervalMinutes > 10080 || f.WindowHours < 1 || f.WindowHours > 720 || f.MinViews < 0 || f.MinAgeMinutes < 0 || f.MinAgeMinutes >= f.WindowHours*60 {
		return fmt.Errorf("%w: неверные фильтры поиска", domain.ErrInvalid)
	}
	for _, v := range []float64{f.MinDurationSec, f.MaxDurationSec, f.CompilationMinDurationSec, f.CompilationMaxDurationSec} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return domain.ErrInvalid
		}
	}
	if f.MinDurationSec <= 0 || f.MaxDurationSec < f.MinDurationSec || f.MaxDurationSec > 60 {
		return fmt.Errorf("%w: длина исходника должна быть до 60 секунд", domain.ErrInvalid)
	}
	if f.CompilationMinClips < 3 || f.CompilationMaxClips < f.CompilationMinClips || f.CompilationMaxClips > 60 || f.CompilationMinDurationSec < 0 || (!f.LegacyLength && f.CompilationMinDurationSec <= 0) || f.CompilationMaxDurationSec < f.CompilationMinDurationSec || f.CompilationMaxDurationSec > 3600 || f.CompilationHour < 0 || f.CompilationHour > 23 {
		return fmt.Errorf("%w: неверные ограничения подборки", domain.ErrInvalid)
	}
	if f.DailyShorts < 1 || f.DailyShorts > 100 || f.DailyCompilations < 1 || f.DailyCompilations > 10 {
		return domain.ErrInvalid
	}
	if _, err := time.LoadLocation(f.Timezone); err != nil {
		return fmt.Errorf("%w: invalid timezone", domain.ErrInvalid)
	}
	for _, slots := range [][]string{f.ShortsSlots, f.CompilationSlots} {
		seen := map[string]bool{}
		if len(slots) > 100 {
			return domain.ErrInvalid
		}
		for _, slot := range slots {
			t, e := time.Parse("15:04", slot)
			if e != nil || t.Format("15:04") != slot || seen[slot] {
				return fmt.Errorf("%w: время слотов должно быть уникальным в формате ЧЧ:ММ", domain.ErrInvalid)
			}
			seen[slot] = true
		}
	}
	if f.AutoPublish && ((f.ShortsEnabled && len(f.ShortsSlots) == 0) || (f.CompilationsEnabled && len(f.CompilationSlots) == 0)) {
		return fmt.Errorf("%w: для автопубликации задайте ежедневные слоты", domain.ErrInvalid)
	}
	return nil
}
func confirmedFlow(ctx context.Context, tx pgx.Tx, f twitch.Flow) error {
	var channel, state, platform string
	if err := tx.QueryRow(ctx, `SELECT platform,COALESCE(remote_account_id,''),connection_state FROM accounts WHERE id=$1`, f.AccountID).Scan(&platform, &channel, &state); err != nil {
		return mapErr(err)
	}
	if platform != "youtube" || state != "connected" || channel == "" || channel != f.YouTubeChannelID {
		return fmt.Errorf("%w: подключите подтверждённый YouTube-канал потока; назначение не изменяется автоматически", domain.ErrConflict)
	}
	return nil
}
func (s *Store) SaveTwitchFlow(ctx context.Context, f twitch.Flow, create bool, now time.Time) (twitch.Flow, error) {
	if err := validateFlow(f); err != nil {
		return f, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return f, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return f, err
	}
	var old twitch.Flow
	var started bool
	if create {
		f.ID = uuid.NewString()
		f.Version = 1
	} else {
		old, err = flowByID(ctx, tx, f.ID)
		if err != nil {
			return f, err
		}
		if f.Version != old.Version {
			return f, domain.ErrConflict
		}
		if err = tx.QueryRow(ctx, `SELECT work_started_at IS NOT NULL FROM twitch_flows WHERE id=$1`, f.ID).Scan(&started); err != nil {
			return f, err
		}
		if started && (old.AccountID != f.AccountID || (f.YouTubeChannelID != "" && old.YouTubeChannelID != f.YouTubeChannelID)) {
			return f, fmt.Errorf("%w: назначение потока закреплено после начала работы", domain.ErrConflict)
		}
	}
	stopping := !create && f.AccountID == old.AccountID && !(!old.Enabled && f.Enabled) && !(!old.AutoPublish && f.AutoPublish) && ((!f.Enabled && !f.AutoPublish) || (old.Enabled && !f.Enabled) || (old.AutoPublish && !f.AutoPublish))
	if stopping {
		f.YouTubeChannelID = old.YouTubeChannelID
	} else if f.AccountID > 0 {
		var channel, state, platform string
		if err = tx.QueryRow(ctx, `SELECT platform,COALESCE(remote_account_id,''),connection_state FROM accounts WHERE id=$1`, f.AccountID).Scan(&platform, &channel, &state); err != nil {
			return f, mapErr(err)
		}
		if platform != "youtube" || channel == "" || state != "connected" {
			return f, fmt.Errorf("%w: сначала подключите YouTube-канал", domain.ErrConflict)
		}
		if started && channel != old.YouTubeChannelID {
			return f, fmt.Errorf("%w: OAuth-аккаунт теперь связан с другим каналом; поток заблокирован", domain.ErrConflict)
		}
		f.YouTubeChannelID = channel
	} else if f.Enabled || f.AutoPublish {
		return f, fmt.Errorf("%w: выберите YouTube-канал", domain.ErrInvalid)
	}
	if f.YouTubeChannelID != "" {
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_channel_limits(youtube_channel_id,timezone,daily_shorts,daily_compilations)VALUES($1,$2,$3,$4) ON CONFLICT(youtube_channel_id)DO UPDATE SET timezone=EXCLUDED.timezone,daily_shorts=EXCLUDED.daily_shorts,daily_compilations=EXCLUDED.daily_compilations`, f.YouTubeChannelID, f.Timezone, f.DailyShorts, f.DailyCompilations); err != nil {
			return f, err
		}
	}
	if f.AutoPublish && !old.AutoPublish {
		f.AutoEnabledAt = &now
	} else {
		f.AutoEnabledAt = old.AutoEnabledAt
	}
	if !create {
		f.Version++
	}
	_, err = tx.Exec(ctx, `INSERT INTO twitch_flows(id,config,version,account_id,youtube_channel_id,enabled,auto_publish,auto_enabled_at,created_at,updated_at)VALUES($1,$2,$3,NULLIF($4,0),$5,$6,$7,$8,$9,$9)
 ON CONFLICT(id)DO UPDATE SET config=EXCLUDED.config,version=EXCLUDED.version,account_id=EXCLUDED.account_id,youtube_channel_id=EXCLUDED.youtube_channel_id,enabled=EXCLUDED.enabled,auto_publish=EXCLUDED.auto_publish,auto_enabled_at=EXCLUDED.auto_enabled_at,updated_at=EXCLUDED.updated_at`, f.ID, mustJSON(f), f.Version, f.AccountID, f.YouTubeChannelID, f.Enabled, f.AutoPublish, f.AutoEnabledAt, now)
	if err != nil {
		return f, err
	}
	// Shared quota controls describe a channel, including all OAuth aliases and flows.
	if _, err = tx.Exec(ctx, `UPDATE twitch_flows SET config=config||jsonb_build_object('daily_shorts',$2::int,'daily_compilations',$3::int,'timezone',$4::text),version=version+CASE WHEN id<>$5 AND(config->>'timezone' IS DISTINCT FROM $4 OR config->>'daily_shorts' IS DISTINCT FROM $2::text OR config->>'daily_compilations' IS DISTINCT FROM $3::text)THEN 1 ELSE 0 END WHERE youtube_channel_id=$1`, f.YouTubeChannelID, f.DailyShorts, f.DailyCompilations, f.Timezone, f.ID); err != nil {
		return f, err
	}
	if !f.Enabled || !f.AutoPublish {
		if err = holdFlowAuto(ctx, tx, f.ID); err != nil {
			return f, err
		}
	}
	if !stopping {
		if err = enforceTwitchCaps(ctx, tx); err != nil {
			return f, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return f, err
	}
	return s.GetTwitchFlow(ctx, f.ID)
}
func holdFlowAuto(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE status='active' AND publication_id IN(SELECT id FROM publications WHERE auto_flow_id=$1 AND started_at IS NULL AND status NOT IN('published','cancelled','needs_attention'))`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE publications SET status='on_hold',auto_hold=TRUE,version=version+1,error='Автопубликация потока выключена' WHERE auto_flow_id=$1 AND started_at IS NULL AND status NOT IN('published','cancelled','needs_attention') AND NOT auto_hold`, id)
	return err
}
func (s *Store) DeleteTwitchFlow(ctx context.Context, id string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE twitch_flows SET archived=TRUE,enabled=FALSE,auto_publish=FALSE,version=version+1,updated_at=$2 WHERE id=$1 AND NOT archived`, id, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	if err = holdFlowAuto(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func syncLegacyFlow(ctx context.Context, tx pgx.Tx, previous, s twitch.Settings) error {
	f, err := flowByID(ctx, tx, "default")
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil || !f.LegacyLength {
		return err
	}
	var worked bool
	if err = tx.QueryRow(ctx, `SELECT work_started_at IS NOT NULL FROM twitch_flows WHERE id='default'`).Scan(&worked); err != nil {
		return err
	}
	targetChanged := previous.TargetAccountID != s.TargetAccountID
	if worked && targetChanged && f.AccountID != s.TargetAccountID {
		return fmt.Errorf("%w: назначение основного потока закреплено; создайте новый поток", domain.ErrConflict)
	}
	changed := false
	setInt := func(before, after int, field *int) {
		if before != after {
			*field = after
			changed = true
		}
	}
	setFloat := func(before, after float64, field *float64) {
		if before != after {
			*field = after
			changed = true
		}
	}
	if previous.Enabled != s.Enabled {
		f.Enabled = s.Enabled
		changed = true
	}
	if targetChanged {
		f.AccountID = s.TargetAccountID
		changed = true
	}
	setInt(previous.PollIntervalMinutes, s.PollIntervalMinutes, &f.PollIntervalMinutes)
	setInt(previous.WindowHours, s.WindowHours, &f.WindowHours)
	setInt(previous.MinViews, s.MinViews, &f.MinViews)
	setInt(previous.MinAgeMinutes, s.MinAgeMinutes, &f.MinAgeMinutes)
	setFloat(previous.MinDurationSec, s.MinDurationSec, &f.MinDurationSec)
	setFloat(previous.MaxDurationSec, s.MaxDurationSec, &f.MaxDurationSec)
	setInt(previous.CompilationMinClips, s.CompilationMinClips, &f.CompilationMinClips)
	setInt(previous.CompilationMaxClips, s.CompilationMaxClips, &f.CompilationMaxClips)
	setInt(previous.CompilationHour, s.CompilationHour, &f.CompilationHour)
	setInt(previous.DailyShorts, s.DailyShorts, &f.DailyShorts)
	setInt(previous.DailyCompilations, s.DailyCompilations, &f.DailyCompilations)
	if previous.Timezone != s.Timezone {
		f.Timezone = s.Timezone
		changed = true
	}
	if !changed {
		return nil
	}
	if targetChanged && f.AccountID > 0 {
		if err = tx.QueryRow(ctx, `SELECT COALESCE(remote_account_id,'')FROM accounts WHERE id=$1`, f.AccountID).Scan(&f.YouTubeChannelID); err != nil {
			return err
		}
	}
	if targetChanged && f.AccountID == 0 {
		f.YouTubeChannelID = ""
	}
	_, err = tx.Exec(ctx, `UPDATE twitch_flows SET config=$1,account_id=NULLIF($2,0),youtube_channel_id=$3,enabled=$4,version=version+1,updated_at=now()WHERE id='default'`, mustJSON(f), f.AccountID, f.YouTubeChannelID, f.Enabled)
	if err != nil {
		return err
	}
	if f.YouTubeChannelID != "" && (targetChanged || previous.Timezone != s.Timezone || previous.DailyShorts != s.DailyShorts || previous.DailyCompilations != s.DailyCompilations) {
		_, err = tx.Exec(ctx, `INSERT INTO twitch_channel_limits(youtube_channel_id,timezone,daily_shorts,daily_compilations)VALUES($1,$2,$3,$4)ON CONFLICT(youtube_channel_id)DO UPDATE SET timezone=EXCLUDED.timezone,daily_shorts=EXCLUDED.daily_shorts,daily_compilations=EXCLUDED.daily_compilations`, f.YouTubeChannelID, f.Timezone, f.DailyShorts, f.DailyCompilations)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE twitch_flows SET config=config||jsonb_build_object('daily_shorts',$2::int,'daily_compilations',$3::int,'timezone',$4::text),version=version+1 WHERE youtube_channel_id=$1 AND id<>'default'`, f.YouTubeChannelID, f.DailyShorts, f.DailyCompilations, f.Timezone)
	}
	return err
}
