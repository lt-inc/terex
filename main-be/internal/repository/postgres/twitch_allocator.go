package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
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
	Recoveries      map[string]string
}

// Build explicit predicates so PostgreSQL can use the category/language
// partial index even after pgx switches a prepared query to a generic plan.
// Values are always parameters; only server-defined SQL fragments are joined.
func allocationCandidateQuery(f twitch.Flow, now time.Time, dailyRemaining int, spentIDs, blockedChannels, recoveryIDs []string, newDemand bool) (string, []any) {
	args := []any{f.ID, f.RankingMode, now, twitch.GrowthStaleAfter(f.PollIntervalMinutes).Seconds(), min(f.WindowHours, f.MaxSourceAgeHours), f.MinAgeMinutes, f.MinViews, f.MinDurationSec, f.MaxDurationSec}
	predicates := []string{
		`fs.flow_id=$1 AND NOT fs.excluded AND NOT i.rejected`,
		`i.media_state NOT IN('ready','expired') AND i.state IN('discovered','upload_failed')`,
		`(i.request->'source'->>'created_at')::timestamptz BETWEEN $3::timestamptz-make_interval(hours=>$5) AND $3::timestamptz-make_interval(mins=>$6)`,
		`COALESCE((i.request->'source'->>'view_count')::bigint,0)>=$7`,
		`COALESCE((i.request->'source'->>'duration_sec')::float8,0) BETWEEN $8 AND $9`,
		`NOT EXISTS(SELECT 1 FROM twitch_download_grants g WHERE g.clip_id=i.twitch_clip_id AND g.state IN('active','consumed'))`,
	}
	parameter := func(value any) string {
		args = append(args, value)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Language != "" {
		predicates = append(predicates, `i.request->'source'->>'language'=`+parameter(f.Language)+` AND i.request->'source'->>'language_source'='clip'`)
	}
	if f.SourceMode == "category" {
		predicates = append(predicates, `i.request->'source'->>'game_id'=`+parameter(f.CategoryID))
	} else if !(f.LegacyLength && len(f.BroadcasterIDs) == 0) {
		predicates = append(predicates, `i.request->'source'->>'broadcaster_id'=ANY(`+parameter(f.BroadcasterIDs)+`::text[])`)
	}
	spentParameter := ""
	if dailyRemaining <= 0 || len(blockedChannels) > 0 {
		spentParameter = parameter(spentIDs)
	}
	if dailyRemaining <= 0 {
		predicates = append(predicates, `i.id=ANY(`+spentParameter+`::text[])`)
	}
	recoveryParameter := parameter(recoveryIDs)
	if !newDemand {
		predicates = append(predicates, `i.id=ANY(`+recoveryParameter+`::text[])`)
	}
	if len(blockedChannels) > 0 {
		predicates = append(predicates, `(i.request->'source'->>'broadcaster_id'<>ALL(`+parameter(blockedChannels)+`::text[]) OR i.id=ANY(`+spentParameter+`::text[]))`)
	}
	return `SELECT i.id,i.request FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id WHERE ` + strings.Join(predicates, " AND ") + ` ORDER BY CASE WHEN i.id=ANY(` + recoveryParameter + `::text[]) THEN 0 ELSE 1 END,` + twitchGrowthOrder("i", "$2", "$3::timestamptz", "$4") + ` LIMIT 1000`, args
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

func allocationNeedsCompilation(af *allocationFlow) bool {
	return af.Flow.CompilationsEnabled && !af.Target.IsZero() &&
		(af.PlannedClips < af.Flow.CompilationMinClips || af.PlannedDuration < af.Flow.CompilationMinDurationSec)
}

// The coordinator owns quota admission. Discovery remains shared metadata; the
// collector receives durable grants and does not pick a competing local quota.
func (s *Store) AllocateTwitchDownloads(ctx context.Context, q twitch.AdmissionRequest, now time.Time) (result twitch.AdmissionResult, failure error) {
	started := time.Now()
	phase := "pool"
	lockMS, planningMS := int64(0), int64(0)
	var planningStarted time.Time
	committed := false
	defer func() {
		if phase == "flow_planning" && !planningStarted.IsZero() {
			planningMS = time.Since(planningStarted).Milliseconds()
		}
		if failure != nil {
			failure = fmt.Errorf("admissions %s: %w", phase, failure)
		}
		slog.InfoContext(ctx, "twitch admissions finished", "phase", phase, "committed", committed, "grants", len(result.Grants), "daily_remaining", result.DailyRemaining, "lock_ms", lockMS, "planning_ms", planningMS, "duration_ms", time.Since(started).Milliseconds(), "err", failure)
	}()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return twitch.AdmissionResult{}, err
	}
	defer tx.Rollback(ctx)
	phase = "coordinator_lock"
	lockStarted := time.Now()
	err = lockTwitch(ctx, tx)
	lockMS = time.Since(lockStarted).Milliseconds()
	if err != nil {
		return twitch.AdmissionResult{}, err
	}
	phase = "settings_and_expiry"
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
	// Redeliver durable unclaimed grants after a collector restart. The local
	// queue deduplicates clip_id; no new download quota is charged.
	if out.Mode == "enforce" {
		rows, err := tx.Query(ctx, `SELECT g.clip_id,g.import_id,g.flow_id,f.version,g.primary_format,i.request,g.epoch,g.sequence,COALESCE(i.revalidation->>'failure_token',''),g.expires_at FROM twitch_download_grants g JOIN twitch_imports i ON i.id=g.import_id JOIN twitch_flows f ON f.id=g.flow_id WHERE g.state='active' AND g.expires_at>$1 AND f.enabled AND NOT f.archived ORDER BY g.sequence LIMIT $2`, now, q.Limit)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var g twitch.DownloadGrant
			var b []byte
			var r twitch.ImportRequest
			if err = rows.Scan(&g.ClipID, &g.ImportID, &g.FlowID, &g.FlowVersion, &g.Format, &b, &g.Epoch, &g.AdmissionSequence, &g.RevalidationToken, &g.ExpiresAt); err != nil {
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
	if out.Mode == "enforce" {
		if e := storagePreparationAvailable(ctx, tx, now); e != nil {
			var wait *domain.PreparationWaitError
			if !errors.As(e, &wait) {
				return out, e
			}
			// Storage availability is shared by every candidate. Rechecking it
			// for thousands of candidates only holds the coordinator until the
			// request times out. Existing grants and accepted uploads survive.
			phase = "storage_wait"
			slog.InfoContext(ctx, "twitch admissions waiting for storage", "reason_code", wait.Code, "reason", wait.Message, "redelivered_grants", len(out.Grants))
			err = tx.Commit(ctx)
			committed = err == nil
			return out, err
		}
	}
	if len(out.Grants) >= q.Limit {
		phase = "redelivery"
		err = tx.Commit(ctx)
		committed = err == nil
		return out, err
	}
	// Read daily source/channel accounting once under the coordinator lock.
	// Candidate rejection must not execute a quota query for every discovery.
	channelImports := map[string]map[string]bool{}
	spentIDs := []string{}
	spent := map[string]bool{}
	quotaRows, err := tx.Query(ctx, `SELECT r.broadcaster_id,r.import_id FROM twitch_download_reservations r WHERE(r.created_at AT TIME ZONE $1)::date=$2::date UNION SELECT COALESCE(i.request->'source'->>'broadcaster_id',''),g.import_id FROM twitch_download_grants g JOIN twitch_imports i ON i.id=g.import_id WHERE(g.created_at AT TIME ZONE $1)::date=$2::date AND g.state<>'released'`, settings.Timezone, day)
	if err != nil {
		return out, err
	}
	for quotaRows.Next() {
		var broadcaster, id string
		if err = quotaRows.Scan(&broadcaster, &id); err != nil {
			quotaRows.Close()
			return out, err
		}
		if channelImports[broadcaster] == nil {
			channelImports[broadcaster] = map[string]bool{}
		}
		channelImports[broadcaster][id] = true
		if !spent[id] {
			spentIDs = append(spentIDs, id)
			spent[id] = true
		}
	}
	quotaRows.Close()
	if err = quotaRows.Err(); err != nil {
		return out, err
	}
	if out.DailyRemaining == 0 {
		// Revalidation of a source already charged today remains quota-exempt.
		// Skip full planning only when no such source can need a new grant.
		var recovery bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_imports i WHERE i.id=ANY($1::text[]) AND NOT i.rejected AND i.state IN('discovered','upload_failed') AND i.media_state NOT IN('ready','expired') AND NOT EXISTS(SELECT 1 FROM twitch_download_grants g WHERE g.clip_id=i.twitch_clip_id AND g.state IN('active','consumed')) AND EXISTS(SELECT 1 FROM twitch_flow_sources fs JOIN twitch_flows f ON f.id=fs.flow_id WHERE fs.import_id=i.id AND NOT fs.excluded AND f.enabled AND NOT f.archived))`, spentIDs).Scan(&recovery); err != nil {
			return out, err
		}
		if !recovery {
			phase = "daily_quota_full"
			if out.Mode == "shadow" {
				return out, nil
			}
			err = tx.Commit(ctx)
			committed = err == nil
			return out, err
		}
	}
	blockedChannels := []string{}
	for broadcaster, ids := range channelImports {
		if len(ids) >= settings.PerChannelDailyDownloads {
			blockedChannels = append(blockedChannels, broadcaster)
		}
	}
	phase = "flow_planning"
	planningStarted = time.Now()
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
		f, e := scanFlow(tx.QueryRow(ctx, `SELECT `+collectorFlowCols+` FROM twitch_flows WHERE id=$1 AND NOT archived`, id))
		if e != nil {
			return out, e
		}
		if confirmedFlow(ctx, tx, f) != nil {
			continue
		}
		af := &allocationFlow{Flow: f, Recoveries: map[string]string{}}
		// An expired unclaimed grant must not strand its still-valid preparation
		// slot. Such sources need re-admission even when that slot fills the
		// calendar or the expected compilation already meets its minimum.
		recoveries, e := tx.Query(ctx, `SELECT ps.import_id,r.format FROM twitch_preparation_sources ps JOIN twitch_preparation_reservations r ON r.id=ps.reservation_id WHERE r.flow_id=$1 AND r.state='active' AND r.clip_id IS NULL AND r.expires_at>$2 ORDER BY r.slot_at,r.id`, id, now)
		if e != nil {
			return out, e
		}
		for recoveries.Next() {
			var importID, format string
			if e = recoveries.Scan(&importID, &format); e != nil {
				recoveries.Close()
				return out, e
			}
			if af.Recoveries[importID] == "" && ((format == "short" && f.ShortsEnabled) || (format == "compilation" && f.CompilationsEnabled)) {
				af.Recoveries[importID] = format
			}
		}
		recoveries.Close()
		if e = recoveries.Err(); e != nil {
			return out, e
		}
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
			ready, _, e := inspectFlowCompilationReadyTx(ctx, tx, f, af.Target, now)
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
		recoveryIDs := []string{}
		for importID := range af.Recoveries {
			recoveryIDs = append(recoveryIDs, importID)
		}
		newDemand := f.ShortsEnabled && af.ShortSlots > 0 || allocationNeedsCompilation(af)
		if !newDemand && len(recoveryIDs) == 0 {
			continue
		}
		candidateSQL, candidateArgs := allocationCandidateQuery(f, now, out.DailyRemaining, spentIDs, blockedChannels, recoveryIDs, newDemand)
		candidates, e := tx.Query(ctx, candidateSQL, candidateArgs...)
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
		// Keep the Twitch/growth ranking within each group; completing existing
		// reservations takes precedence over reserving more calendar capacity.
		sort.SliceStable(af.Candidates, func(i, j int) bool {
			return af.Recoveries[af.Candidates[i].ImportID] != "" && af.Recoveries[af.Candidates[j].ImportID] == ""
		})
		flows = append(flows, af)
	}

	planningMS = time.Since(planningStarted).Milliseconds()
	phase = "selection_and_reservation"
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
			recovering := af.Recoveries[af.Candidates[0].ImportID]
			if af.Flow.CompilationsEnabled && !af.Target.IsZero() && (recovering == "compilation" || (recovering == "" && allocationNeedsCompilation(af))) {
				comp = append(comp, af)
			}
			if af.Flow.ShortsEnabled && (recovering == "short" || (recovering == "" && af.ShortSlots > 0)) {
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
		channelCount := len(channelImports[g.Source.BroadcasterID])
		alreadySpent := channelImports[g.Source.BroadcasterID][g.ImportID]
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
			expiry := now.Add(15 * time.Minute)
			g.ExpiresAt = &expiry
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
			if channelImports[g.Source.BroadcasterID] == nil {
				channelImports[g.Source.BroadcasterID] = map[string]bool{}
			}
			channelImports[g.Source.BroadcasterID][g.ImportID] = true
		}
		out.DailyRemaining = max(0, settings.DailyDownloads-total)
		if format == "compilation" {
			if !alreadySpent {
				compUsed++
			}
			if af.Recoveries[g.ImportID] == "" {
				if af.PlannedClips > 0 {
					af.PlannedDuration++
				}
				af.PlannedDuration += g.Source.DurationSec
				af.PlannedClips++
			}
		} else if af.Recoveries[g.ImportID] == "" {
			af.ShortSlots--
		}
		reason := "reserved_pending_commit"
		if af.Recoveries[g.ImportID] != "" {
			reason = "recovery_pending_commit"
		}
		slog.InfoContext(ctx, "twitch preparation decision", "flow_id", g.FlowID, "format", format, "mode", out.Mode, "budget_percent", settings.CompilationBudgetPercent, "reason", reason, "grant_count", 1, "daily_remaining", out.DailyRemaining)
	}
	if out.Mode == "shadow" {
		slog.InfoContext(ctx, "twitch preparation decision", "mode", out.Mode, "budget_percent", settings.CompilationBudgetPercent, "reason", "shadow_allocation", "grant_count", len(out.Grants), "daily_remaining", out.DailyRemaining)
		return out, nil
	}
	phase = "commit"
	err = tx.Commit(ctx)
	committed = err == nil
	return out, err
}
