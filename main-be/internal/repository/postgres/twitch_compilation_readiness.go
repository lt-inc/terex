package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"math"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

// Shared by diagnostics and execution. This inspection does not reserve work.
func nextFlowCompilationTargetTx(ctx context.Context, tx pgx.Tx, f twitch.Flow, now time.Time) (time.Time, error) {
	var at time.Time
	err := tx.QueryRow(ctx, `SELECT slot_at FROM twitch_preparation_reservations WHERE flow_id=$1 AND format='compilation' AND state='active' AND clip_id IS NULL AND expires_at>$2 AND slot_at>$2 ORDER BY slot_at LIMIT 1`, f.ID, now).Scan(&at)
	if err == nil {
		return at, nil
	}
	if err != pgx.ErrNoRows {
		return at, err
	}
	slots, err := calendarSlotsTx(ctx, tx, f, "compilation", 0, "", now)
	if err != nil {
		return at, err
	}
	for _, slot := range slots {
		if !slot.Occupied && !slot.Preparing {
			var exists bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_compilations WHERE flow_id=$1 AND local_date=$2::date AND timezone=$3)`, f.ID, slot.At.In(mustLocation(f.Timezone)).Format("2006-01-02"), f.Timezone).Scan(&exists); err != nil {
				return at, err
			}
			if !exists {
				return slot.At, nil
			}
		}
	}
	return at, nil
}

func inspectFlowCompilationTx(ctx context.Context, tx pgx.Tx, f twitch.Flow, targetAt, now time.Time) (domain.AutoCompilationResult, []twitch.CompilationItemRequest, error) {
	return inspectFlowCompilationSourcesTx(ctx, tx, f, targetAt, now, false)
}

// Admission needs the canonical ready selection, not a tally of every discovery.
func inspectFlowCompilationReadyTx(ctx context.Context, tx pgx.Tx, f twitch.Flow, targetAt, now time.Time) (domain.AutoCompilationResult, []twitch.CompilationItemRequest, error) {
	return inspectFlowCompilationSourcesTx(ctx, tx, f, targetAt, now, true)
}

func inspectFlowCompilationSourcesTx(ctx context.Context, tx pgx.Tx, f twitch.Flow, targetAt, now time.Time, readyOnly bool) (domain.AutoCompilationResult, []twitch.CompilationItemRequest, error) {
	readyFilter := ""
	if readyOnly {
		readyFilter = " AND i.media_state='ready' AND NOT i.rejected AND NOT fs.excluded AND m.id IS NOT NULL AND m.storage_state='available'"
	}
	target := targetAt
	if target.IsZero() {
		target = now
	}
	out := domain.AutoCompilationResult{Timezone: f.Timezone, LocalDate: target.In(mustLocation(f.Timezone)).Format("2006-01-02"), MinimumClips: f.CompilationMinClips, WindowHours: f.WindowHours, MinimumDurationSec: f.CompilationMinDurationSec, ScheduledAt: targetAt, Sources: &domain.CompilationSourceCounts{}}
	rows, err := tx.Query(ctx, `SELECT i.id,COALESCE(m.id,''),COALESCE(i.request->'source'->>'broadcaster_id',''),COALESCE((m.verified->>'duration_sec')::float8,0),fs.excluded OR i.rejected,CASE WHEN m.storage_state IS NOT NULL AND m.storage_state<>'available' THEN 'expired' ELSE i.media_state END,EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=$2 AND u.format='compilation' AND u.media_id=m.id),i.request,EXISTS(SELECT 1 FROM twitch_preparation_sources ps JOIN twitch_preparation_reservations pr ON pr.id=ps.reservation_id WHERE ps.import_id=i.id AND pr.youtube_channel_id=$2 AND pr.format='compilation' AND pr.state='active' AND pr.expires_at>$3 AND(pr.flow_id<>$1 OR pr.clip_id IS NOT NULL)) FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id LEFT JOIN twitch_media m ON m.id=i.media_id WHERE fs.flow_id=$1`+readyFilter+` ORDER BY `+twitchGrowthOrder("i", "$4", "$3::timestamptz", "$5"), f.ID, f.YouTubeChannelID, now, f.RankingMode, twitch.GrowthStaleAfter(f.PollIntervalMinutes).Seconds())
	if err != nil {
		return out, nil, err
	}
	pool := []flowCandidate{}
	for rows.Next() {
		var c flowCandidate
		var rejected, used, reserved bool
		var state string
		var b []byte
		var req twitch.ImportRequest
		if err = rows.Scan(&c.ID, &c.Media, &c.Broadcaster, &c.Duration, &rejected, &state, &used, &b, &reserved); err != nil {
			rows.Close()
			return out, nil, err
		}
		if err = json.Unmarshal(b, &req); err != nil {
			rows.Close()
			return out, nil, err
		}
		n := out.Sources
		before := n.Eligible
		tallyCompilationSource(n, f, req.Source, target, now, rejected, used || reserved, state, c.Media, c.Duration)
		if n.Eligible > before {
			pool = append(pool, c)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, nil, err
	}
	items := selectFlowCandidates(pool, f)
	out.SelectedClips = len(items)
	for _, v := range items {
		out.ReadyDurationSec += math.Ceil(v.EndSec*30-1e-7) / 30
	}
	if len(items) > 1 {
		out.ReadyDurationSec += float64(len(items) - 1)
	}
	out.MissingDurationSec = math.Max(0, f.CompilationMinDurationSec-out.ReadyDurationSec)
	switch {
	case !f.Enabled || !f.CompilationsEnabled:
		out.Reason = "collection_disabled"
		out.Message = "Подборки потока выключены"
	case f.CompilationMinDurationSec > float64(f.CompilationMaxClips)*f.MaxDurationSec+float64(f.CompilationMaxClips-1)+0.05:
		out.Reason = "impossible_compilation_limits"
		out.Message = fmt.Sprintf("Минимальная длина %.0f сек недостижима: максимум %d клипов по %.0f сек дают %.0f сек с переходами. Уменьшите минимум или увеличьте число/длину клипов.", f.CompilationMinDurationSec, f.CompilationMaxClips, f.MaxDurationSec, float64(f.CompilationMaxClips)*f.MaxDurationSec+float64(f.CompilationMaxClips-1))
	case targetAt.IsZero():
		out.Reason = "calendar_full"
		out.Message = "В ближайшем календаре нет места для подборки"
	case len(items) < f.CompilationMinClips || out.ReadyDurationSec < f.CompilationMinDurationSec:
		out.Reason = "insufficient_eligible_sources"
		out.Message = fmt.Sprintf("Готово %d клипов (%.0f сек с переходами). Нужно минимум %d клипов и %.0f сек; не хватает %.0f сек. Ожидаемые скачивания не включены в готовый состав.", len(items), out.ReadyDurationSec, f.CompilationMinClips, f.CompilationMinDurationSec, out.MissingDurationSec)
	default:
		out.Reason = "ready"
		out.Message = "Материал готов для сборки"
	}
	next := now.Add(time.Minute)
	out.NextAttemptAt = &next
	return out, items, nil
}

func (s *Store) InspectFlowCompilation(ctx context.Context, id string, now time.Time) (domain.AutoCompilationResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AutoCompilationResult{}, err
	}
	defer tx.Rollback(ctx)
	f, err := flowByID(ctx, tx, id)
	if err != nil {
		return domain.AutoCompilationResult{}, err
	}
	at, err := nextFlowCompilationTargetTx(ctx, tx, f, now)
	if err != nil {
		return domain.AutoCompilationResult{}, err
	}
	out, _, err := inspectFlowCompilationTx(ctx, tx, f, at, now)
	return out, err
}

func (s *Store) AutoFlowCompilation(ctx context.Context, id string, now time.Time) (domain.AutoCompilationResult, error) {
	// Operator responses retain the full source breakdown. Periodic preparation
	// only needs ready physical media and must not scan every pending candidate.
	return s.autoFlowCompilation(ctx, id, now, false)
}

func (s *Store) autoFlowCompilation(ctx context.Context, id string, now time.Time, readyOnly bool) (domain.AutoCompilationResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AutoCompilationResult{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return domain.AutoCompilationResult{}, err
	}
	var f twitch.Flow
	if readyOnly {
		f, err = scanFlow(tx.QueryRow(ctx, `SELECT `+collectorFlowCols+` FROM twitch_flows WHERE id=$1 AND NOT archived`, id))
	} else {
		f, err = flowByID(ctx, tx, id)
	}
	if err != nil {
		return domain.AutoCompilationResult{}, err
	}
	if err = twitchResetAllowed(ctx, tx); err != nil {
		return domain.AutoCompilationResult{}, err
	}
	if err = expirePreparationTx(ctx, tx, now); err != nil {
		return domain.AutoCompilationResult{}, err
	}
	target, err := nextFlowCompilationTargetTx(ctx, tx, f, now)
	if err != nil {
		return domain.AutoCompilationResult{}, err
	}
	var out domain.AutoCompilationResult
	var items []twitch.CompilationItemRequest
	if readyOnly {
		out, items, err = inspectFlowCompilationReadyTx(ctx, tx, f, target, now)
	} else {
		out, items, err = inspectFlowCompilationTx(ctx, tx, f, target, now)
	}
	if err != nil {
		return out, err
	}
	if err = confirmedFlow(ctx, tx, f); err != nil {
		out.Reason = "target_account_missing"
		out.Message = err.Error()
		return out, nil
	}
	if target.IsZero() {
		return out, nil
	}
	local := target.In(mustLocation(f.Timezone))
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, mustLocation(f.Timezone))
	var existing string
	err = tx.QueryRow(ctx, `SELECT clip_id FROM twitch_compilations WHERE flow_id=$1 AND local_date=$2 AND timezone=$3`, id, day, f.Timezone).Scan(&existing)
	if err == nil {
		c, e := compilationByID(ctx, tx, existing)
		out.Compilation = &c
		out.Reason = "daily_draft_exists"
		out.Message = "Черновик для этой даты выпуска уже существует"
		return out, e
	}
	if err != pgx.ErrNoRows {
		return out, err
	}
	if out.Reason != "ready" {
		return out, nil
	}
	req := twitch.CompilationRequest{FlowID: id, Title: f.Name + " | " + day.Format("02.01.2006"), Items: items}
	if len([]rune(req.Title)) > 100 {
		req.Title = string([]rune(req.Title)[:100])
	}
	c, err := createFlowCompilationTx(ctx, tx, req, &day, now)
	if err != nil {
		return out, err
	}
	out.Created = true
	out.Compilation = &c
	out.Reason = "created"
	out.Message = "Подборка собрана и отправлена на обработку"
	return out, tx.Commit(ctx)
}
