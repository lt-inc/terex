package postgres

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"math"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

// Diagnostics deliberately uses no admission/reservation operations. Polling
// this endpoint must not consume quota or mutate the next scheduling decision.
func (s *Store) TwitchFlowDiagnostics(ctx context.Context, id string, now time.Time) (domain.TwitchFlowDiagnostics, error) {
	out := domain.TwitchFlowDiagnostics{FlowID: id, GeneratedAt: now, Sources: map[string]int{}, Competitors: []domain.TwitchDiagnosticCompetitor{}, Messages: []string{}, PendingClipIDs: []string{}, PendingStates: map[string]string{}}
	// One read-only connection, one flow read and one full catalogue pass. The
	// previous composition repeatedly loaded flow counters, calendars and every
	// source before returning even a single diagnostic observation.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	f, err := scanFlow(tx.QueryRow(ctx, `SELECT `+collectorFlowCols+` FROM twitch_flows WHERE id=$1 AND NOT archived`, id))
	if err != nil {
		return out, err
	}
	at, err := nextFlowCompilationTargetTx(ctx, tx, f, now)
	if err != nil {
		return out, err
	}
	out.Compilation, _, err = inspectFlowCompilationReadyTx(ctx, tx, f, at, now)
	if err != nil {
		return out, err
	}
	out.Compilation.Sources = &domain.CompilationSourceCounts{}
	out.Capacity, err = flowCapacityTx(ctx, tx, f, now)
	if err != nil {
		return out, err
	}
	target := out.Compilation.ScheduledAt
	if target.IsZero() {
		target = now
	}
	rows, err := tx.Query(ctx, `SELECT i.twitch_clip_id,i.request->'source',i.media_state,i.state,fs.excluded OR i.rejected,COALESCE((m.verified->>'duration_sec')::float8,0),COALESCE(m.storage_state,''),COALESCE(m.id,''),EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=$2 AND u.format='compilation' AND u.media_id=i.media_id),EXISTS(SELECT 1 FROM twitch_preparation_sources ps JOIN twitch_preparation_reservations pr ON pr.id=ps.reservation_id WHERE ps.import_id=i.id AND pr.youtube_channel_id=$2 AND pr.format='compilation' AND pr.state='active' AND pr.expires_at>$3 AND(pr.flow_id<>$1 OR pr.clip_id IS NOT NULL)) FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id LEFT JOIN twitch_media m ON m.id=i.media_id WHERE fs.flow_id=$1`, id, f.YouTubeChannelID, now)
	if err != nil {
		return out, err
	}
	totalDuration := 0.0
	readyCount := 0
	for rows.Next() {
		var clip, media, state, storage, mediaID string
		var raw []byte
		var excluded, used, reserved bool
		var duration float64
		if err = rows.Scan(&clip, &raw, &media, &state, &excluded, &duration, &storage, &mediaID, &used, &reserved); err != nil {
			rows.Close()
			return out, err
		}
		var src twitch.Source
		if err = json.Unmarshal(raw, &src); err != nil {
			rows.Close()
			return out, err
		}
		// Keep the existing compilation count priority while sharing this pass
		// with the more detailed source-state breakdown below.
		compilationMedia := media
		if storage != "" && storage != "available" {
			compilationMedia = "expired"
		}
		tallyCompilationSource(out.Compilation.Sources, f, src, target, now, excluded, used || reserved, compilationMedia, mediaID, duration)
		bucket := ""
		switch {
		case excluded:
			bucket = "excluded"
		case f.Language != "" && (src.Language != f.Language || src.LanguageSource != "clip"):
			if src.Language == "" || src.LanguageSource != "clip" {
				bucket = "language_unknown"
			} else {
				bucket = "language_mismatch"
			}
		case !sourceMatches(f, src):
			bucket = "source_mismatch"
		case !preparationSourceFresh(f, src, target, now) || src.CreatedAt.Before(now.Add(-time.Duration(f.WindowHours)*time.Hour)):
			bucket = "age_filtered"
		case src.ViewCount < f.MinViews:
			bucket = "views_filtered"
		case src.CreatedAt.After(now.Add(-time.Duration(f.MinAgeMinutes) * time.Minute)):
			bucket = "too_new"
		case src.DurationSec < f.MinDurationSec || src.DurationSec > f.MaxDurationSec:
			bucket = "duration_filtered"
		case media == "expired" || state == "expired":
			bucket = "expired"
		case used || reserved:
			bucket = "already_used"
		case media == "ready" && storage == "available" && duration >= f.MinDurationSec && duration <= f.MaxDurationSec+0.05:
			bucket = "ready"
			totalDuration += duration
			readyCount++
		case media == "ready":
			bucket = "duration_filtered"
		case state == "failed":
			bucket = "failed"
		case state == "upload_pending":
			bucket = "verifying"
			out.PendingClipIDs = append(out.PendingClipIDs, clip)
		default:
			bucket = "need_download"
			out.PendingClipIDs = append(out.PendingClipIDs, clip)
		}
		out.Sources[bucket]++
		if bucket == "verifying" || bucket == "need_download" {
			out.PendingStates[clip] = bucket
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.AverageDurationSec = 30
	out.AverageEstimated = readyCount == 0
	if readyCount > 0 {
		out.AverageDurationSec = totalDuration / float64(readyCount)
	}
	if !out.Compilation.ScheduledAt.IsZero() {
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(ps.duration_sec),0),count(DISTINCT i.id)::integer FROM twitch_preparation_sources ps JOIN twitch_preparation_reservations r ON r.id=ps.reservation_id JOIN twitch_imports i ON i.id=ps.import_id JOIN twitch_flow_sources fs ON fs.import_id=i.id AND fs.flow_id=r.flow_id WHERE r.flow_id=$1 AND r.format='compilation' AND r.state='active' AND r.clip_id IS NULL AND r.expires_at>$2 AND r.slot_at=$3 AND i.media_state<>'ready' AND NOT i.rejected AND NOT fs.excluded AND i.twitch_clip_id=ANY($4::text[])`, id, now, out.Compilation.ScheduledAt, out.PendingClipIDs).Scan(&out.ExpectedDurationSec, &out.ExpectedClips); err != nil {
			return out, err
		}
	}
	expectedWithTransitions := out.ExpectedDurationSec + float64(out.ExpectedClips)
	out.EstimatedAdditionalClips = max(0, int(math.Ceil((out.Compilation.MissingDurationSec-expectedWithTransitions)/(out.AverageDurationSec+1))))
	out.EstimatedAdditionalClips = max(out.EstimatedAdditionalClips, max(0, f.CompilationMinClips-out.Compilation.SelectedClips-out.ExpectedClips))
	out.Feasibility = "ready"
	if out.Compilation.MissingDurationSec > 0 || out.Compilation.SelectedClips < f.CompilationMinClips {
		out.Feasibility = "waiting_verification"
	}
	if out.EstimatedAdditionalClips > 0 {
		out.Feasibility = "needs_sources"
	}
	if out.Compilation.Reason == "calendar_full" {
		out.Feasibility = "calendar_full"
	}
	if !f.Enabled || !f.CompilationsEnabled {
		out.Feasibility = "paused"
	}
	if possible := float64(f.CompilationMaxClips)*f.MaxDurationSec + float64(max(0, f.CompilationMaxClips-1)); possible < f.CompilationMinDurationSec {
		out.Feasibility = "impossible_limits"
		out.Messages = append(out.Messages, "Минимальная длина недостижима при текущем максимуме клипов и длине исходника. Уменьшите минимальную длину подборки или увеличьте максимум клипов")
	}
	rows, err = tx.Query(ctx, `SELECT f.id,COALESCE(f.config->>'name',f.id),x.format,sum(x.scheduled)::integer,sum(x.preparing)::integer,min(x.at) FROM twitch_flows f JOIN (
 SELECT o.flow_id,o.format,1 AS scheduled,0 AS preparing,p.publish_at AS at FROM twitch_outputs o JOIN publications p ON p.clip_id=o.clip_id WHERE o.youtube_channel_id=$1 AND p.status NOT IN('cancelled','on_hold','failed','blocked') AND p.publish_at>=$2 AND p.publish_at<=$3
 UNION ALL SELECT r.flow_id,r.format,0,1,r.slot_at FROM twitch_preparation_reservations r WHERE r.youtube_channel_id=$1 AND r.state='active' AND r.expires_at>$2 AND r.slot_at<=$3 AND NOT EXISTS(SELECT 1 FROM publications p WHERE p.clip_id=r.clip_id AND p.status NOT IN('cancelled','on_hold','failed','blocked'))
 ) x ON x.flow_id=f.id GROUP BY f.id,x.format ORDER BY min(x.at),f.id`, f.YouTubeChannelID, now, out.Capacity.HorizonEnd)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.TwitchDiagnosticCompetitor
		if err = rows.Scan(&v.FlowID, &v.Name, &v.Format, &v.Scheduled, &v.Preparing, &v.NearestAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Competitors = append(out.Competitors, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	if out.Compilation.Message != "" {
		out.Messages = append(out.Messages, out.Compilation.Message)
	}
	if out.EstimatedAdditionalClips > 0 {
		out.Messages = append(out.Messages, "Для выпуска нужны новые исходники; оценка количества зависит от их длины, фильтров и доступного бюджета")
	}
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return out, err
	}
	out.Collector = domain.TwitchDiagnosticCollector{Timezone: settings.Timezone, DailyLimit: settings.DailyDownloads, DailyUnlimited: settings.DownloadsUnlimited(), CompilationBudgetPercent: settings.CompilationBudgetPercent, AdmissionMode: settings.AdmissionMode, ByFlow: []domain.TwitchDiagnosticBudgetFlow{}}
	rows, err = tx.Query(ctx, `WITH spent AS(SELECT import_id FROM twitch_download_reservations WHERE(created_at AT TIME ZONE $1)::date=$2::date UNION SELECT import_id FROM twitch_download_grants WHERE(created_at AT TIME ZONE $1)::date=$2::date AND state<>'released') SELECT COALESCE(g.flow_id,NULLIF(i.request->>'flow_id',''),'legacy'),COALESCE(f.config->>'name','Прежний поток'),COALESCE(g.primary_format,'shared'),count(*)::integer FROM spent s JOIN twitch_imports i ON i.id=s.import_id LEFT JOIN twitch_download_grants g ON g.import_id=i.id AND g.state<>'released' LEFT JOIN twitch_flows f ON f.id=COALESCE(g.flow_id,NULLIF(i.request->>'flow_id','')) GROUP BY COALESCE(g.flow_id,NULLIF(i.request->>'flow_id',''),'legacy'),COALESCE(f.config->>'name','Прежний поток'),COALESCE(g.primary_format,'shared') ORDER BY count(*) DESC`, settings.Timezone, now.In(mustLocation(settings.Timezone)).Format("2006-01-02"))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var v domain.TwitchDiagnosticBudgetFlow
		if err = rows.Scan(&v.FlowID, &v.Name, &v.Format, &v.Spent); err != nil {
			rows.Close()
			return out, err
		}
		out.Collector.Spent += v.Spent
		out.Collector.ByFlow = append(out.Collector.ByFlow, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Collector.Remaining = settings.RemainingDownloads(out.Collector.Spent)
	return out, nil
}

func tallyCompilationSource(n *domain.CompilationSourceCounts, f twitch.Flow, src twitch.Source, target, now time.Time, rejected, used bool, state, media string, duration float64) {
	n.Total++
	switch {
	case rejected:
		n.Rejected++
	case !preparationSourceFresh(f, src, target, now) || src.CreatedAt.Before(now.Add(-time.Duration(f.WindowHours)*time.Hour)):
		n.OutsideWindow++
	case f.Language != "" && (src.Language != f.Language || src.LanguageSource != "clip"):
		n.Filtered++
		n.LanguageFiltered++
	case !sourceMatches(f, src):
		n.Filtered++
	case used:
		n.AlreadyUsed++
	case state != "ready" || media == "":
		n.NotReady++
		switch state {
		case "failed":
			n.Failed++
		case "expired":
			n.Expired++
		case "verifying":
			n.Verifying++
		case "pending":
			n.DownloadPending++
		default:
			n.DownloadPending++
		}
	case duration < f.MinDurationSec || duration > f.MaxDurationSec+0.05:
		n.Filtered++
		n.DurationFiltered++
	case !sourceEligible(f, src, now):
		n.Filtered++
	default:
		n.Eligible++
	}
}
