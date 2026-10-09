package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sort"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

type allocationFlow struct {
	Flow            twitch.Flow
	Candidates      []twitch.DownloadGrant
	Target          time.Time
	PlannedDuration float64
	PlannedClips    int
	LastSequence    int64
	ShortSlots      int
}

func allocationFormat(compUsed, totalUsed, percent int, hasComp, hasShort bool) string {
	if hasComp && (!hasShort || compUsed*100 < (totalUsed+1)*percent) {
		return "compilation"
	}
	if hasShort {
		return "short"
	}
	return ""
}

// The coordinator owns quota admission. Discovery remains shared metadata; the
// collector receives durable grants and does not pick a competing local quota.
func (s *Store) AllocateTwitchDownloads(ctx context.Context, q twitch.AdmissionRequest, now time.Time) (twitch.AdmissionResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return twitch.AdmissionResult{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return twitch.AdmissionResult{}, err
	}
	if err = twitchResetRequestAllowed(ctx, tx, q.Epoch); err != nil {
		return twitch.AdmissionResult{}, err
	}
	settings, err := readTwitchSettings(ctx, tx)
	if err != nil {
		return twitch.AdmissionResult{}, err
	}
	out := twitch.AdmissionResult{Mode: settings.AdmissionMode, BudgetPercent: settings.CompilationBudgetPercent, Grants: []twitch.DownloadGrant{}}
	if out.Mode == "" {
		out.Mode = "shadow"
	}
	if !settings.Enabled {
		return out, nil
	}
	if err = expirePreparationTx(ctx, tx, now); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_download_grants g SET state=CASE WHEN i.media_state='ready' OR i.download_reserved_at IS NOT NULL THEN 'consumed' ELSE 'released' END FROM twitch_imports i,twitch_flows f WHERE i.id=g.import_id AND f.id=g.flow_id AND g.state='active' AND(g.expires_at<=$1 OR i.media_state='ready' OR i.state IN('failed','expired') OR i.download_reserved_at IS NOT NULL OR NOT f.enabled OR f.archived)`, now); err != nil {
		return out, err
	}
	day := now.In(mustLocation(settings.Timezone)).Format("2006-01-02")
	var total, compUsed int
	if err = tx.QueryRow(ctx, `WITH spent AS(SELECT import_id FROM twitch_download_reservations WHERE(created_at AT TIME ZONE $1)::date=$2::date UNION SELECT import_id FROM twitch_download_grants WHERE(created_at AT TIME ZONE $1)::date=$2::date AND state<>'released') SELECT count(*),(SELECT count(*) FROM twitch_download_grants g WHERE g.import_id IN(SELECT import_id FROM spent)AND g.primary_format='compilation') FROM spent`, settings.Timezone, day).Scan(&total, &compUsed); err != nil {
		return out, err
	}
	out.DailyRemaining = max(0, settings.DailyDownloads-total)
	var backlog int
	if err = tx.QueryRow(ctx, `SELECT count(DISTINCT COALESCE(i.media_id,i.id))FROM twitch_imports i WHERE `+pendingTwitchSource).Scan(&backlog); err != nil {
		return out, err
	}
	if backlog >= settings.BacklogLimit {
		return out, nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM twitch_flows WHERE enabled AND NOT archived ORDER BY created_at,id`)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
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
	flows := []*allocationFlow{}
	for _, id := range ids {
		f, e := flowByID(ctx, tx, id)
		if e != nil {
			return out, e
		}
		if confirmedFlow(ctx, tx, f) != nil {
			continue
		}
		af := &allocationFlow{Flow: f}
		if f.ShortsEnabled {
			slots, e := calendarSlotsTx(ctx, tx, f, "short", 0, "", now)
			if e != nil {
				return out, e
			}
			for _, slot := range slots {
				if !slot.Occupied && !slot.Preparing {
					af.ShortSlots++
				}
			}
		}
		if e = tx.QueryRow(ctx, `SELECT COALESCE(max(sequence),0) FROM twitch_download_grants WHERE flow_id=$1`, id).Scan(&af.LastSequence); e != nil {
			return out, e
		}
		if f.CompilationsEnabled {
			af.Target, e = nextFlowCompilationTargetTx(ctx, tx, f, now)
			if e != nil {
				return out, e
			}
			ready, _, e := inspectFlowCompilationTx(ctx, tx, f, af.Target, now)
			if e != nil {
				return out, e
			}
			if ready.Reason == "impossible_compilation_limits" {
				af.Target = time.Time{}
			}
			af.PlannedDuration = ready.ReadyDurationSec
			af.PlannedClips = ready.SelectedClips
			// Only unfinished sources add expected duration. Ready sources already
			// appeared in the canonical selection and must not be counted twice.
			var waitingDuration float64
			var waitingCount int
			pending, e := tx.Query(ctx, `SELECT i.request,ps.duration_sec FROM twitch_preparation_sources ps JOIN twitch_preparation_reservations r ON r.id=ps.reservation_id JOIN twitch_imports i ON i.id=ps.import_id JOIN twitch_flow_sources fs ON fs.import_id=i.id AND fs.flow_id=r.flow_id WHERE r.flow_id=$1 AND r.format='compilation' AND r.state='active' AND r.clip_id IS NULL AND r.expires_at>$2 AND r.slot_at=$3 AND i.media_state NOT IN('ready','expired') AND i.state NOT IN('failed','expired') AND NOT i.rejected AND NOT fs.excluded`, id, now, af.Target)
			if e != nil {
				return out, e
			}
			for pending.Next() {
				var b []byte
				var d float64
				var req twitch.ImportRequest
				if e = pending.Scan(&b, &d); e != nil {
					pending.Close()
					return out, e
				}
				if e = json.Unmarshal(b, &req); e != nil {
					pending.Close()
					return out, e
				}
				if sourceEligible(f, req.Source, now) && preparationSourceFresh(f, req.Source, af.Target, now) {
					waitingCount++
					waitingDuration += d
				}
			}
			pending.Close()
			if e = pending.Err(); e != nil {
				return out, e
			}
			if waitingCount > 0 && af.PlannedClips > 0 {
				waitingDuration++
			}
			if waitingCount > 1 {
				waitingDuration += float64(waitingCount - 1)
			}
			af.PlannedDuration += waitingDuration
			af.PlannedClips += waitingCount
		}
		candidates, e := tx.Query(ctx, `SELECT i.id,i.request FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id WHERE fs.flow_id=$1 AND NOT fs.excluded AND NOT i.rejected AND i.media_state NOT IN('ready','expired') AND i.state IN('discovered','upload_failed') AND (i.request->'source'->>'created_at')::timestamptz BETWEEN $3::timestamptz-make_interval(hours=>$5) AND $3::timestamptz-make_interval(mins=>$6) AND COALESCE((i.request->'source'->>'view_count')::bigint,0)>=$7 AND COALESCE((i.request->'source'->>'duration_sec')::float8,0) BETWEEN $8 AND $9 AND($10='' OR(i.request->'source'->>'language'=$10 AND i.request->'source'->>'language_source'='clip')) AND(($11='category' AND i.request->'source'->>'game_id'=$12) OR($11<>'category' AND(i.request->'source'->>'broadcaster_id'=ANY($13::text[]) OR $14))) AND NOT EXISTS(SELECT 1 FROM twitch_download_grants g WHERE g.clip_id=i.twitch_clip_id AND g.state IN('active','consumed')) ORDER BY `+twitchGrowthOrder("i", "$2", "$3::timestamptz", "$4")+` LIMIT 1000`, id, f.RankingMode, now, twitch.GrowthStaleAfter(f.PollIntervalMinutes).Seconds(), min(f.WindowHours, f.MaxSourceAgeHours), f.MinAgeMinutes, f.MinViews, f.MinDurationSec, f.MaxDurationSec, f.Language, f.SourceMode, f.CategoryID, f.BroadcasterIDs, f.LegacyLength && len(f.BroadcasterIDs) == 0)
		if e != nil {
			return out, e
		}
		for candidates.Next() {
			var iid string
			var b []byte
			var r twitch.ImportRequest
			if e = candidates.Scan(&iid, &b); e != nil {
				candidates.Close()
				return out, e
			}
			if e = json.Unmarshal(b, &r); e != nil {
				candidates.Close()
				return out, e
			}
			if sourceEligible(f, r.Source, now) {
				af.Candidates = append(af.Candidates, twitch.DownloadGrant{ClipID: r.Source.ClipID, ImportID: iid, FlowID: id, FlowVersion: f.Version, Source: r.Source, Epoch: q.Epoch})
			}
		}
		candidates.Close()
		if e = candidates.Err(); e != nil {
			return out, e
		}
		flows = append(flows, af)
	}
	// Redeliver durable unclaimed grants after a collector restart. The local
	// queue deduplicates clip_id; no new download quota is charged.
	if out.Mode == "enforce" {
		rows, err = tx.Query(ctx, `SELECT g.clip_id,g.import_id,g.flow_id,f.version,g.primary_format,i.request,g.epoch,g.sequence,COALESCE(i.revalidation->>'failure_token','') FROM twitch_download_grants g JOIN twitch_imports i ON i.id=g.import_id JOIN twitch_flows f ON f.id=g.flow_id WHERE g.state='active' AND g.expires_at>$1 AND f.enabled AND NOT f.archived ORDER BY g.sequence LIMIT $2`, now, q.Limit)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var g twitch.DownloadGrant
			var b []byte
			var r twitch.ImportRequest
			if err = rows.Scan(&g.ClipID, &g.ImportID, &g.FlowID, &g.FlowVersion, &g.Format, &b, &g.Epoch, &g.AdmissionSequence, &g.RevalidationToken); err != nil {
				rows.Close()
				return out, err
			}
			if err = json.Unmarshal(b, &r); err != nil {
				rows.Close()
				return out, err
			}
			g.Source = r.Source
			out.Grants = append(out.Grants, g)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return out, err
		}
	}
	selected := map[string]bool{}
	for _, g := range out.Grants {
		selected[g.ClipID] = true
	}
	// A revalidated source already charged today can receive its replacement
	// grant even after the distinct-source budget is exhausted.
	targetCount := q.Limit
	for len(out.Grants) < targetCount {
		comp, shorts := []*allocationFlow{}, []*allocationFlow{}
		for _, af := range flows {
			for len(af.Candidates) > 0 && selected[af.Candidates[0].ClipID] {
				af.Candidates = af.Candidates[1:]
			}
			if len(af.Candidates) == 0 {
				continue
			}
			if af.Flow.CompilationsEnabled && !af.Target.IsZero() && (af.PlannedClips < af.Flow.CompilationMinClips || af.PlannedDuration < af.Flow.CompilationMinDurationSec) {
				comp = append(comp, af)
			}
			if af.Flow.ShortsEnabled && af.ShortSlots > 0 {
				shorts = append(shorts, af)
			}
		}
		format := allocationFormat(compUsed, total, settings.CompilationBudgetPercent, len(comp) > 0, len(shorts) > 0)
		if format == "" {
			break
		}
		pool := shorts
		if format == "compilation" {
			pool = comp
		}
		sort.SliceStable(pool, func(i, j int) bool {
			a, b := pool[i], pool[j]
			if format == "compilation" {
				if !a.Target.Equal(b.Target) {
					return a.Target.Before(b.Target)
				}
				da, db := math.Max(0, a.Flow.CompilationMinDurationSec-a.PlannedDuration), math.Max(0, b.Flow.CompilationMinDurationSec-b.PlannedDuration)
				if math.Abs(da-db) > 0.05 {
					return da < db
				}
			}
			if a.LastSequence != b.LastSequence {
				return a.LastSequence < b.LastSequence
			}
			return a.Flow.ID < b.Flow.ID
		})
		af := pool[0]
		g := af.Candidates[0]
		af.Candidates = af.Candidates[1:]
		g.Format = format
		var channelCount int
		var alreadySpent bool
		if err = tx.QueryRow(ctx, `SELECT count(*),COALESCE(bool_or(import_id=$4),FALSE) FROM(SELECT r.import_id FROM twitch_download_reservations r WHERE r.broadcaster_id=$3 AND(r.created_at AT TIME ZONE $1)::date=$2::date UNION SELECT g.import_id FROM twitch_download_grants g JOIN twitch_imports i ON i.id=g.import_id WHERE i.request->'source'->>'broadcaster_id'=$3 AND(g.created_at AT TIME ZONE $1)::date=$2::date AND g.state<>'released') x`, settings.Timezone, day, g.Source.BroadcasterID, g.ImportID).Scan(&channelCount, &alreadySpent); err != nil {
			return out, err
		}
		if !alreadySpent && (channelCount >= settings.PerChannelDailyDownloads || out.DailyRemaining == 0) {
			continue
		}
		if !preparationSourceFresh(af.Flow, g.Source, af.Target, now) && format == "compilation" {
			continue
		}
		if out.Mode == "enforce" {
			_, ok, e := reservePreparationFormat(ctx, tx, af.Flow, g.ImportID, format, g.Source, now)
			if e != nil {
				if errors.Is(e, domain.ErrConflict) {
					continue
				}
				return out, e
			}
			if !ok {
				if format == "short" {
					// Another flow may have filled this channel's shared calendar.
					// A clip too old for a free slot must not disable all Shorts.
					slots, e := calendarSlotsTx(ctx, tx, af.Flow, "short", 0, "", now)
					if e != nil {
						return out, e
					}
					af.ShortSlots = 0
					for _, slot := range slots {
						if !slot.Occupied && !slot.Preparing {
							af.ShortSlots++
						}
					}
					if af.ShortSlots == 0 {
						// Borrow a full calendar's unused share without losing this candidate.
						af.Candidates = append([]twitch.DownloadGrant{g}, af.Candidates...)
					}
				}
				continue
			}
			if err = tx.QueryRow(ctx, `INSERT INTO twitch_download_grants(clip_id,import_id,flow_id,primary_format,epoch,created_at,expires_at)VALUES($1,$2,$3,$4,$5,$6,$7)ON CONFLICT(clip_id)DO UPDATE SET flow_id=EXCLUDED.flow_id,primary_format=EXCLUDED.primary_format,epoch=EXCLUDED.epoch,created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at,state='active',sequence=nextval('twitch_download_grants_sequence_seq') WHERE twitch_download_grants.state='released' RETURNING sequence`, g.ClipID, g.ImportID, g.FlowID, g.Format, q.Epoch, now, now.Add(15*time.Minute)).Scan(&af.LastSequence); err != nil {
				return out, err
			}
			g.AdmissionSequence = af.LastSequence
			if err = tx.QueryRow(ctx, `SELECT COALESCE(revalidation->>'failure_token','') FROM twitch_imports WHERE id=$1`, g.ImportID).Scan(&g.RevalidationToken); err != nil {
				return out, err
			}
		} else {
			af.LastSequence++
		}
		selected[g.ClipID] = true
		out.Grants = append(out.Grants, g)
		if !alreadySpent {
			total++
		}
		out.DailyRemaining = max(0, settings.DailyDownloads-total)
		if format == "compilation" {
			if !alreadySpent {
				compUsed++
			}
			if af.PlannedClips > 0 {
				af.PlannedDuration++
			}
			af.PlannedDuration += g.Source.DurationSec
			af.PlannedClips++
		} else {
			af.ShortSlots--
		}
		slog.InfoContext(ctx, "twitch preparation decision", "flow_id", g.FlowID, "format", format, "mode", out.Mode, "budget_percent", settings.CompilationBudgetPercent, "reason", "granted", "grant_count", 1, "daily_remaining", out.DailyRemaining)
	}
	if out.Mode == "shadow" {
		slog.InfoContext(ctx, "twitch preparation decision", "mode", out.Mode, "budget_percent", settings.CompilationBudgetPercent, "reason", "shadow_allocation", "grant_count", len(out.Grants), "daily_remaining", out.DailyRemaining)
		return out, nil
	}
	return out, tx.Commit(ctx)
}
