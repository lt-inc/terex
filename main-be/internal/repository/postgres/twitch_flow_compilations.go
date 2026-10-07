package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"math"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/msg"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

func freezeFlowAssembly(ctx context.Context, tx pgx.Tx, req twitch.CompilationRequest, f twitch.Flow, owner string) (workflow.SourceManifest, error) {
	m := workflow.SourceManifest{Options: msg.JobOptions{Vertical: false, Subtitles: false}, Cues: []workflow.Cue{}, Assembly: &workflow.AssemblySpec{SchemaVersion: 2, Profile: "landscape_1080p30", BumperDurationFrames: 30, Items: []workflow.AssemblyItem{}}}
	if strings.TrimSpace(req.Title) == "" || len([]rune(req.Title)) > 100 || len(req.Items) < f.CompilationMinClips || len(req.Items) > f.CompilationMaxClips {
		return m, fmt.Errorf("%w: задайте название и от %d до %d исходников", domain.ErrInvalid, f.CompilationMinClips, f.CompilationMaxClips)
	}
	seen := map[string]bool{}
	for _, item := range req.Items {
		var b, vb []byte
		var id, media string
		var excluded bool
		err := tx.QueryRow(ctx, `SELECT i.id,i.request,m.id,m.verified,fs.excluded OR i.rejected FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id JOIN twitch_media m ON m.id=i.media_id WHERE fs.flow_id=$1 AND m.storage_state='available' AND i.media_state='ready' AND(( $2<>'' AND i.id=$2)OR($2='' AND i.clip_id=$3))`, f.ID, item.SourceID, item.ClipID).Scan(&id, &b, &media, &vb, &excluded)
		if err != nil {
			return m, mapErr(err)
		}
		if excluded || seen[media] {
			return m, fmt.Errorf("%w: исходник исключён или повторяется", domain.ErrConflict)
		}
		seen[media] = true
		var used bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_source_uses WHERE youtube_channel_id=$1 AND format='compilation' AND media_id=$2 AND($3='' OR clip_id IS DISTINCT FROM $3))`, f.YouTubeChannelID, media, owner).Scan(&used); err != nil {
			return m, err
		}
		if used {
			return m, fmt.Errorf("%w: исходник уже использован в подборке этого YouTube-канала", domain.ErrConflict)
		}
		var r twitch.ImportRequest
		var v workflow.VerifiedMedia
		if json.Unmarshal(b, &r) != nil || json.Unmarshal(vb, &v) != nil {
			return m, domain.ErrInvalid
		}
		if err = storageRefsAvailable(ctx, tx, v.Original); err != nil {
			return m, err
		}
		start, end := item.StartSec, item.EndSec
		if end == 0 {
			end = v.DurationSec
		}
		if math.IsNaN(start) || math.IsNaN(end) || math.IsInf(start, 0) || math.IsInf(end, 0) || start < 0 || end <= start || end > v.DurationSec+0.05 {
			return m, domain.ErrInvalid
		}
		m.Assembly.Items = append(m.Assembly.Items, workflow.AssemblyItem{TwitchSourceID: id, MediaID: media, Original: v.Original, SHA256: v.SHA256, StartSec: start, EndSec: end, BroadcasterLogin: r.Source.BroadcasterLogin})
		m.DurationSec += math.Ceil((end-start)*30-1e-7) / 30
	}
	m.DurationSec += float64(len(req.Items) - 1)
	if m.DurationSec > f.CompilationMaxDurationSec+0.05 {
		return m, fmt.Errorf("%w: подборка с переходами превышает максимальную длину", domain.ErrInvalid)
	}
	return m, nil
}
func reserveAssemblyUse(ctx context.Context, tx pgx.Tx, f twitch.Flow, clip string, m workflow.SourceManifest, now time.Time) error {
	for _, item := range m.Assembly.Items {
		if _, err := tx.Exec(ctx, `INSERT INTO twitch_source_uses(youtube_channel_id,format,media_id,clip_id,created_at)VALUES($1,'compilation',$2,$3,$4)ON CONFLICT DO NOTHING`, f.YouTubeChannelID, item.MediaID, clip, now); err != nil {
			return err
		}
	}
	return nil
}
func createFlowCompilationTx(ctx context.Context, tx pgx.Tx, req twitch.CompilationRequest, day *time.Time, now time.Time) (domain.Compilation, error) {
	f, err := flowByID(ctx, tx, req.FlowID)
	if err != nil {
		return domain.Compilation{}, err
	}
	if err = confirmedFlow(ctx, tx, f); err != nil {
		return domain.Compilation{}, err
	}
	if !f.CompilationsEnabled {
		return domain.Compilation{}, fmt.Errorf("%w: подборки в потоке выключены", domain.ErrConflict)
	}
	m, err := freezeFlowAssembly(ctx, tx, req, f, "")
	if err != nil {
		return domain.Compilation{}, err
	}
	ids := []string{}
	for _, item := range m.Assembly.Items {
		ids = append(ids, item.TwitchSourceID)
	}
	if _, err = reserveOutputPreparationTx(ctx, tx, f, "", "compilation", ids, now); err != nil {
		return domain.Compilation{}, err
	}
	id, jid := "compilation-"+uuid.NewString(), uuid.NewString()
	rid, err := createTwitchDraft(ctx, tx, jid, id, "twitch_compilation", "compilation", "", "", req.Title, f.AccountID, f.Timezone, m, now)
	if err != nil {
		return domain.Compilation{}, err
	}
	desc, err := assemblyProvenance(ctx, tx, m.Assembly)
	if err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET spec=jsonb_set(spec,'{description}',to_jsonb($2::text))WHERE id=$1`, rid, desc); err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_compilations(clip_id,flow_id,local_date,timezone,created_at)VALUES($1,$2,$3,$4,$5)`, id, f.ID, day, f.Timezone, now); err != nil {
		return domain.Compilation{}, err
	}
	if err = saveAssemblyItems(ctx, tx, rid, m.Assembly); err != nil {
		return domain.Compilation{}, err
	}
	if err = reserveAssemblyUse(ctx, tx, f, id, m, now); err != nil {
		return domain.Compilation{}, err
	}
	auto := day != nil && f.AutoPublish && f.AutoEnabledAt != nil && !now.Before(*f.AutoEnabledAt)
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_outputs(clip_id,flow_id,account_id,youtube_channel_id,format,config,initial_revision_id,auto_eligible,created_at)VALUES($1,$2,$3,$4,'compilation',$5,$6,$7,$8)`, id, f.ID, f.AccountID, f.YouTubeChannelID, mustJSON(f), rid, auto, now); err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_preparation_reservations r SET clip_id=$2 WHERE r.flow_id=$1 AND r.format='compilation' AND r.state='active' AND r.clip_id IS NULL AND NOT EXISTS(SELECT 1 FROM unnest($3::text[]) x WHERE NOT EXISTS(SELECT 1 FROM twitch_preparation_sources ps WHERE ps.reservation_id=r.id AND ps.import_id=x))`, f.ID, id, ids); err != nil {
		return domain.Compilation{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_flows SET work_started_at=COALESCE(work_started_at,$2)WHERE id=$1`, f.ID, now); err != nil {
		return domain.Compilation{}, err
	}
	return compilationByID(ctx, tx, id)
}

type flowCandidate struct {
	ID, Media, Broadcaster string
	Duration               float64
}

// Views order is preserved within each broadcaster. Balance is soft: exhausted
// broadcasters yield their time to others, and only whole clips fit the budget.
func selectFlowCandidates(pool []flowCandidate, f twitch.Flow) []twitch.CompilationItemRequest {
	out := []twitch.CompilationItemRequest{}
	spent := map[string]float64{}
	seen := map[string]bool{}
	duration := 0.0
	for len(out) < f.CompilationMaxClips {
		pick := -1
		for n, c := range pool {
			extra := math.Ceil(c.Duration*30-1e-7) / 30
			if len(out) > 0 {
				extra++
			}
			if seen[c.Media] || c.ID == "" || duration+extra > f.CompilationMaxDurationSec+0.05 {
				continue
			}
			if pick < 0 || (f.BalanceByTime && spent[c.Broadcaster] < spent[pool[pick].Broadcaster]) {
				pick = n
			}
		}
		if pick < 0 {
			break
		}
		c := pool[pick]
		seen[c.Media] = true
		spent[c.Broadcaster] += c.Duration
		if len(out) > 0 {
			duration++
		}
		duration += math.Ceil(c.Duration*30-1e-7) / 30
		out = append(out, twitch.CompilationItemRequest{SourceID: c.ID, EndSec: c.Duration})
	}
	return out
}
func (s *Store) AutoFlowCompilation(ctx context.Context, id string, now time.Time) (domain.AutoCompilationResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AutoCompilationResult{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return domain.AutoCompilationResult{}, err
	}
	f, err := flowByID(ctx, tx, id)
	if err != nil {
		return domain.AutoCompilationResult{}, err
	}
	loc, _ := time.LoadLocation(f.Timezone)
	local := now.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	out := domain.AutoCompilationResult{Timezone: f.Timezone, LocalDate: day.Format("2006-01-02"), MinimumClips: f.CompilationMinClips, WindowHours: f.WindowHours, MinimumDurationSec: f.CompilationMinDurationSec, ScheduledAt: day.Add(time.Duration(f.CompilationHour) * time.Hour)}
	if !f.Enabled || !f.CompilationsEnabled {
		out.Reason = "collection_disabled"
		out.Message = "Подборки потока выключены"
		return out, nil
	}
	if err = confirmedFlow(ctx, tx, f); err != nil {
		out.Reason = "target_account_missing"
		out.Message = err.Error()
		return out, nil
	}
	if now.Before(out.ScheduledAt) {
		out.Reason = "before_scheduled_hour"
		out.Message = "Подборка будет подготовлена после " + out.ScheduledAt.Format("15:04")
		out.NextAttemptAt = &out.ScheduledAt
		return out, nil
	}
	var existing string
	err = tx.QueryRow(ctx, `SELECT clip_id FROM twitch_compilations WHERE flow_id=$1 AND local_date=$2 AND timezone=$3`, id, day, f.Timezone).Scan(&existing)
	if err == nil {
		c, e := compilationByID(ctx, tx, existing)
		out.Compilation = &c
		out.Reason = "daily_draft_exists"
		out.Message = "Сегодняшний автоматический черновик уже подготовлен"
		return out, e
	}
	if err != pgx.ErrNoRows {
		return out, err
	}
	mode, err := policyMode(ctx, tx)
	if err != nil {
		return out, err
	}
	var batch string
	var targetAt time.Time
	if mode == "enforce" {
		if err = expirePreparationTx(ctx, tx, now); err != nil {
			return out, err
		}
		err = tx.QueryRow(ctx, `SELECT id,slot_at FROM twitch_preparation_reservations WHERE flow_id=$1 AND format='compilation' AND state='active' AND clip_id IS NULL AND expires_at>$2 ORDER BY slot_at LIMIT 1`, id, now).Scan(&batch, &targetAt)
		if err != nil && err != pgx.ErrNoRows {
			return out, err
		}
		if batch == "" {
			slots, e := calendarSlotsTx(ctx, tx, f, "compilation", 0, "", now)
			if e != nil {
				return out, e
			}
			for _, s := range slots {
				if !s.Occupied && !s.Preparing {
					targetAt = s.At
					break
				}
			}
		}
		if targetAt.IsZero() {
			out.Reason = "calendar_full"
			out.Message = "Календарь ближайших подборок заполнен; новый рендер не создаётся"
			next := now.Add(time.Minute)
			out.NextAttemptAt = &next
			return out, nil
		}
	}
	rows, err := tx.Query(ctx, `SELECT i.id,m.id,COALESCE(i.request->'source'->>'broadcaster_id',''),COALESCE((m.verified->>'duration_sec')::float8,0),fs.excluded OR i.rejected,CASE WHEN m.storage_state='available' THEN i.media_state ELSE 'expired' END,(i.request->'source'->>'created_at')::timestamptz,EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=$2 AND u.format='compilation' AND u.media_id=m.id),i.request FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id LEFT JOIN twitch_media m ON m.id=i.media_id WHERE fs.flow_id=$1 AND($3='' OR EXISTS(SELECT 1 FROM twitch_preparation_sources ps WHERE ps.import_id=i.id AND ps.reservation_id=$3)) ORDER BY COALESCE((i.request->'source'->>'view_count')::bigint,0) DESC,fs.created_at,i.id`, id, f.YouTubeChannelID, batch)
	if err != nil {
		return out, err
	}
	counts := &domain.CompilationSourceCounts{}
	pool := []flowCandidate{}
	for rows.Next() {
		var c flowCandidate
		var media *string
		var rejected, used bool
		var state string
		var created time.Time
		var request []byte
		var src twitch.ImportRequest
		if err = rows.Scan(&c.ID, &media, &c.Broadcaster, &c.Duration, &rejected, &state, &created, &used, &request); err != nil {
			rows.Close()
			return out, err
		}
		if err = json.Unmarshal(request, &src); err != nil {
			rows.Close()
			return out, err
		}
		if media != nil {
			c.Media = *media
		}
		counts.Total++
		switch {
		case rejected:
			counts.Rejected++
		case state != "ready" || media == nil || c.Duration < f.MinDurationSec || c.Duration > f.MaxDurationSec+0.05:
			counts.NotReady++
		case created.Before(now.Add(-time.Duration(f.WindowHours) * time.Hour)):
			counts.OutsideWindow++
		case used:
			counts.AlreadyUsed++
		case !sourceEligible(f, src.Source, now):
			counts.Filtered++
		case mode == "enforce" && !preparationSourceFresh(f, src.Source, targetAt, now):
			counts.Filtered++
		default:
			counts.Eligible++
			pool = append(pool, c)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	out.Sources = counts
	items := selectFlowCandidates(pool, f)
	duration := float64(len(items) - 1)
	for _, v := range items {
		duration += math.Ceil(v.EndSec*30-1e-7) / 30
	}
	out.ReadyDurationSec = math.Max(0, duration)
	out.MissingDurationSec = math.Max(0, f.CompilationMinDurationSec-out.ReadyDurationSec)
	if len(items) < f.CompilationMinClips || duration < f.CompilationMinDurationSec {
		out.Reason = "insufficient_eligible_sources"
		out.Message = fmt.Sprintf("Готово %d исходников, подборка %.0f сек. Ждём минимум %d исходников и %.0f сек; максимум %.0f сек с переходами.", counts.Eligible, math.Max(0, duration), f.CompilationMinClips, f.CompilationMinDurationSec, f.CompilationMaxDurationSec)
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
	out.Message = "Подборка подготовлена и отправлена на обработку"
	return out, tx.Commit(ctx)
}
