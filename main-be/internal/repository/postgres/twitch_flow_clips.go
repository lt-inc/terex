package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

func (s *Store) ListFlowClips(ctx context.Context, id string, q domain.FlowClipQuery) (domain.FlowClipPage, error) {
	out := domain.FlowClipPage{Items: []domain.FlowClip{}, Limit: q.Limit, Offset: q.Offset}
	if out.Limit <= 0 {
		out.Limit = 50
	}
	if out.Limit > 50 {
		out.Limit = 50
	}
	if out.Offset < 0 {
		out.Offset = 0
	}
	f, e := s.GetTwitchFlow(ctx, id)
	if e != nil {
		return out, e
	}
	now := time.Now().UTC()
	target := now
	if q.State == "age_filtered" {
		inspection, err := s.InspectFlowCompilation(ctx, id, now)
		if err != nil {
			return out, err
		}
		if !inspection.ScheduledAt.IsZero() {
			target = inspection.ScheduledAt
		}
	}
	const base = ` FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id JOIN twitch_flows current_flow ON current_flow.id=fs.flow_id
 WHERE fs.flow_id=$1 AND($2='' OR i.request->'source'->>'broadcaster_id'=$2)
 AND($8 OR $7='' OR(COALESCE(i.request->'source'->>'language','')=$7 AND i.request->'source'->>'language_source'='clip'))
 AND($3='' OR concat(i.request->'source'->>'title',' ',i.request->'source'->>'broadcaster_login',' ',i.twitch_clip_id) ILIKE '%'||$3||'%')
 AND($4='' OR EXISTS(SELECT 1 FROM twitch_flows fl WHERE fl.id=fs.flow_id AND COALESCE((fl.config->>($4||'s_enabled'))::boolean,false)) OR EXISTS(SELECT 1 FROM twitch_outputs o WHERE o.flow_id=fs.flow_id AND o.format=$4 AND(o.source_id=i.id OR o.clip_id IN(SELECT r.clip_id FROM twitch_compilation_items ci JOIN clip_revisions r ON r.id=ci.compilation_revision_id WHERE ci.source_id=i.id))))
 AND($5='' OR($5='language_mismatch' AND $7<>'' AND i.request->'source'->>'language_source'='clip' AND COALESCE(i.request->'source'->>'language','') NOT IN('',$7))
 OR($5='language_unknown' AND $7<>'' AND(COALESCE(i.request->'source'->>'language','')='' OR COALESCE(i.request->'source'->>'language_source','')<>'clip'))
 OR($5 IN('filtered','source_mismatch') AND($7='' OR(i.request->'source'->>'language'=$7 AND i.request->'source'->>'language_source'='clip')) AND CASE WHEN current_flow.config->>'source_mode'='category' THEN COALESCE(i.request->'source'->>'game_id','')<>COALESCE(current_flow.config->>'category_id','') ELSE NOT((COALESCE((current_flow.config->>'legacy_length')::boolean,false) AND jsonb_array_length(COALESCE(NULLIF(current_flow.config->'broadcaster_ids','null'::jsonb),'[]'::jsonb))=0) OR COALESCE(NULLIF(current_flow.config->'broadcaster_ids','null'::jsonb),'[]'::jsonb) ? COALESCE(i.request->'source'->>'broadcaster_id','')) END)
 OR($5='age_filtered' AND ((i.request->'source'->>'created_at')::timestamptz < now()-COALESCE((current_flow.config->>'window_hours')::integer,72)*interval '1 hour' OR (i.request->'source'->>'created_at')::timestamptz < $9::timestamptz-COALESCE(NULLIF((current_flow.config->>'max_source_age_hours')::integer,0),72)*interval '1 hour'))
 OR($5='views_filtered' AND COALESCE((i.request->'source'->>'view_count')::bigint,0)<COALESCE((current_flow.config->>'min_views')::integer,0))
 OR($5='too_new' AND (i.request->'source'->>'created_at')::timestamptz>now()-COALESCE((current_flow.config->>'min_age_minutes')::integer,0)*interval '1 minute')
 OR($5='duration_filtered' AND(COALESCE((i.verified->>'duration_sec')::float8,(i.request->'source'->>'duration_sec')::float8)<COALESCE((current_flow.config->>'min_duration_sec')::float8,5) OR COALESCE((i.verified->>'duration_sec')::float8,(i.request->'source'->>'duration_sec')::float8)>COALESCE((current_flow.config->>'max_duration_sec')::float8,60)))
 OR $5=CASE WHEN i.media_state='expired' OR i.state='expired' THEN 'media_expired' WHEN fs.excluded OR i.rejected THEN 'excluded' WHEN i.media_state='ready' AND EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=fs.config->>'youtube_channel_id' AND u.media_id=i.media_id AND($4='' OR u.format=$4)) THEN 'used' WHEN i.media_state='ready' THEN 'ready' WHEN i.state='failed' THEN 'failed' WHEN i.state='upload_pending' THEN 'downloading' ELSE 'discovered' END
 OR(NOT fs.excluded AND NOT i.rejected AND i.media_state NOT IN('ready','expired') AND i.state<>'expired' AND i.twitch_clip_id=ANY($6::text[]))
 OR($5='failed' AND EXISTS(SELECT 1 FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id LEFT JOIN publications p ON p.clip_id=o.clip_id WHERE o.flow_id=fs.flow_id AND($4='' OR o.format=$4) AND(o.source_id=i.id OR EXISTS(SELECT 1 FROM twitch_compilation_items ci WHERE ci.compilation_revision_id=r.id AND ci.source_id=i.id)) AND(r.render_state='failed' OR p.status IN('failed','blocked','needs_attention')))))`
	args := []any{id, q.BroadcasterID, q.Q, q.Format, q.State, q.CollectorSourceIDs, f.Language, q.ShowFiltered, target}
	if e := s.pool.QueryRow(ctx, `SELECT count(*)`+base, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	args = append(args, out.Limit, out.Offset)
	rows, e := s.pool.Query(ctx, `SELECT i.id,i.request,fs.excluded,i.rejected,COALESCE(i.media_id,''),i.media_state,i.state,COALESCE(i.error,''),i.upload_claim_until,EXISTS(SELECT 1 FROM twitch_storage_pins pin WHERE pin.kind='import' AND pin.target_id=i.id)`+base+` ORDER BY fs.created_at DESC,i.id LIMIT $10 OFFSET $11`, args...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var row domain.FlowClip
		var req []byte
		var mediaState, state, errorMsg string
		var until *time.Time
		if e = rows.Scan(&row.ImportID, &req, &row.Excluded, &row.Rejected, &row.MediaID, &mediaState, &state, &errorMsg, &until, &row.Keep); e != nil {
			rows.Close()
			return out, e
		}
		var r twitch.ImportRequest
		if e = json.Unmarshal(req, &r); e != nil {
			rows.Close()
			return out, e
		}
		row.Source = r.Source
		row.SourceClipID = row.ImportID
		row.Outputs = []domain.FlowClipOutput{}
		row.Reason.Actions = []string{}
		row.MediaAvailable = mediaState == "ready"
		switch {
		case mediaState == "expired" || state == "expired":
			row.SourceState = "media_expired"
			row.Reason.Code = "media_expired"
			row.Reason.Message = "Файл удалён по сроку хранения; история и ссылки на публикации сохранены"
		case row.Rejected:
			row.SourceState = "excluded"
			row.Reason.Code = "globally_rejected"
			row.Reason.Message = "Исходник исключён глобально для всех потоков"
		case row.Excluded:
			row.SourceState = "excluded"
			row.Reason.Code = "excluded"
			row.Reason.Message = "Исходник исключён из этого потока"
			row.Reason.Actions = []string{"include"}
		case mediaState == "ready":
			row.SourceState = "ready"
			row.Reason.Code = "ready"
			row.Reason.Message = "Исходник загружен и проверен"
		case state == "failed":
			row.SourceState = "failed"
			row.Reason.Code = "download_failed"
			row.Reason.Message = errorMsg
			row.Reason.Actions = []string{"retry", "exclude"}
		case state == "upload_pending":
			row.SourceState = "downloading"
			row.Reason.Code = "upload_pending"
			row.Reason.Message = "Исходник в очереди загрузки или проверяется"
			row.Reason.NextAttemptAt = until
			row.Reason.Actions = []string{"exclude"}
		default:
			row.SourceState = "discovered"
			row.Reason.Code = "discovered"
			row.Reason.Message = "Найден, ожидает общего бюджета скачивания"
			row.Reason.Actions = []string{"exclude"}
		}
		out.Items = append(out.Items, row)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return out, e
	}
	for n := range out.Items {
		row := &out.Items[n]
		ors, e := s.pool.Query(ctx, `SELECT o.clip_id,c.latest_revision_id,o.format,r.render_state,c.approval,COALESCE(p.status,''),p.publish_at,COALESCE(r.error,p.error,'') FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id LEFT JOIN publications p ON p.clip_id=o.clip_id AND p.account_id=o.account_id WHERE o.flow_id=$1 AND(o.source_id=$2 OR EXISTS(SELECT 1 FROM twitch_compilation_items ci WHERE ci.compilation_revision_id=r.id AND ci.source_id=$2)) ORDER BY o.created_at`, id, row.ImportID)
		if e != nil {
			return out, e
		}
		for ors.Next() {
			var v domain.FlowClipOutput
			if e = ors.Scan(&v.ClipID, &v.RevisionID, &v.Format, &v.RenderState, &v.Approval, &v.PublicationState, &v.PublishAt, &v.Error); e != nil {
				ors.Close()
				return out, e
			}
			row.Outputs = append(row.Outputs, v)
		}
		ors.Close()
		if e = ors.Err(); e != nil {
			return out, e
		}
		if row.SourceState != "media_expired" && !row.Rejected && !row.Excluded && (len(row.Outputs) == 0 || q.State == "filtered" || q.State == "source_mismatch" || q.State == "age_filtered" || (f.Language != "" && (row.Source.Language != f.Language || row.Source.LanguageSource != "clip"))) && (!sourceEligible(f, row.Source, now) || !preparationSourceFresh(f, row.Source, target, now)) {
			row.SourceState = "filtered"
			row.Reason.Code = "filtered"
			row.Reason.Message = sourceFilterMessage(f, row.Source, now)
			if q.State == "age_filtered" && preparationSourceFresh(f, row.Source, now, now) && !preparationSourceFresh(f, row.Source, target, now) {
				row.Reason.Message = fmt.Sprintf("К ближайшей дате подборки %s (%s) клип будет старше допустимых %d часов", target.In(mustLocation(f.Timezone)).Format("02.01 15:04"), f.Timezone, f.MaxSourceAgeHours)
			}
			row.Reason.Actions = []string{"exclude"}
		}
		if row.SourceState == "ready" && len(row.Outputs) > 0 {
			row.SourceState = "used"
			row.Reason.Code = "output_created"
			row.Reason.Message = "Ролик подготовлен из этого исходника"
		}
		if row.SourceState == "ready" && len(row.Outputs) == 0 && f.ShortsEnabled {
			title := publicTwitchTitle(strings.TrimSpace(row.Source.Title + " | " + row.Source.BroadcasterLogin))
			if len([]rune(title)) > 100 {
				title = string([]rune(title)[:100])
			}
			metadata := twitch.GenerateMetadata(flowMetadataInput(f, "short", title, []twitch.Source{row.Source}))
			if len(metadata.Errors) > 0 {
				row.Reason.Code = "metadata_invalid"
				row.Reason.Message = "Файл проверен; выпуск ждёт исправления описания и хештегов потока: " + strings.Join(metadata.Errors, "; ")
				row.Reason.Actions = []string{"retry"}
			}
		}
	}
	return out, nil
}
func sourceFilterMessage(f twitch.Flow, src twitch.Source, now time.Time) string {
	switch {
	case f.Language != "" && (src.Language == "" || src.LanguageSource != "clip"):
		return fmt.Sprintf("Нужен язык %s; Twitch не сообщил язык этого клипа. Язык текущего эфира не подтверждает язык клипа", f.Language)
	case f.Language != "" && f.Language != src.Language:
		return fmt.Sprintf("Нужен язык %s, у клипа %s (данные Twitch)", f.Language, src.Language)
	case !sourceMatches(f, src):
		return "Исходник не соответствует каналам, категории или языку потока"
	case src.ViewCount < f.MinViews:
		return fmt.Sprintf("%d просмотров; требуется минимум %d", src.ViewCount, f.MinViews)
	case src.CreatedAt.After(now.Add(-time.Duration(f.MinAgeMinutes) * time.Minute)):
		return fmt.Sprintf("Исходник слишком новый; минимальный возраст %d мин", f.MinAgeMinutes)
	case src.CreatedAt.Before(now.Add(-time.Duration(f.WindowHours) * time.Hour)):
		return fmt.Sprintf("Исходник вне окна последних %d часов", f.WindowHours)
	case !preparationSourceFresh(f, src, now, now):
		age := f.MaxSourceAgeHours
		if age == 0 {
			age = 72
		}
		return fmt.Sprintf("Клип уже старше допустимых %d часов для публикации", age)
	default:
		return fmt.Sprintf("Длина исходника должна быть от %.0f до %.0f сек", f.MinDurationSec, f.MaxDurationSec)
	}
}
func (s *Store) ActFlowSource(ctx context.Context, flow, id, action string, excluded bool, now time.Time, expectedGeneration ...int64) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = lockTwitch(ctx, tx); e != nil {
		return e
	}
	if _, e = flowByID(ctx, tx, flow); e != nil {
		return e
	}
	var old bool
	if e = tx.QueryRow(ctx, `SELECT excluded FROM twitch_flow_sources WHERE flow_id=$1 AND import_id=$2 FOR UPDATE`, flow, id).Scan(&old); e != nil {
		return mapErr(e)
	}
	switch action {
	case "exclude":
		if _, e = tx.Exec(ctx, `UPDATE twitch_flow_sources SET excluded=$3 WHERE flow_id=$1 AND import_id=$2`, flow, id, excluded); e != nil {
			return e
		}
		if excluded {
			if _, e = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE status='active' AND publication_id IN(SELECT p.id FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id JOIN clips c ON c.clip_id=o.clip_id WHERE o.flow_id=$1 AND p.started_at IS NULL AND(o.source_id=$2 OR EXISTS(SELECT 1 FROM twitch_compilation_items ci WHERE ci.compilation_revision_id=c.latest_revision_id AND ci.source_id=$2)))`, flow, id); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE publications p SET status='on_hold',version=version+1,error='Исходник исключён из потока',auto_hold=FALSE FROM twitch_outputs o,clips c WHERE o.clip_id=p.clip_id AND c.clip_id=o.clip_id AND o.flow_id=$1 AND p.started_at IS NULL AND p.status NOT IN('published','cancelled') AND(o.source_id=$2 OR EXISTS(SELECT 1 FROM twitch_compilation_items ci WHERE ci.compilation_revision_id=c.latest_revision_id AND ci.source_id=$2))`, flow, id); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE twitch_outputs o SET auto_eligible=FALSE WHERE o.flow_id=$1 AND(o.source_id=$2 OR EXISTS(SELECT 1 FROM twitch_compilation_items ci JOIN clips c ON c.latest_revision_id=ci.compilation_revision_id WHERE c.clip_id=o.clip_id AND ci.source_id=$2))`, flow, id); e != nil {
				return e
			}
		} else {
			if e = createFlowShort(ctx, tx, id, flow, now); e != nil {
				return e
			}
		}
	case "retry":
		if old {
			return fmt.Errorf("%w: сначала верните исходник в поток", domain.ErrConflict)
		}
		if len(expectedGeneration) > 0 {
			var generation int64
			if e = tx.QueryRow(ctx, `SELECT upload_generation FROM twitch_imports WHERE id=$1 FOR UPDATE`, id).Scan(&generation); e != nil {
				return e
			}
			if generation != expectedGeneration[0] {
				return domain.ErrConflict
			}
		}
		// The next reservation advances the generation; the collector retains the
		// pinned consumer and can resume the failed task without changing identity.
		if _, e = tx.Exec(ctx, `UPDATE twitch_imports SET upload_claim_until=NULL,upload_expires_at=NULL,state='discovered',error=NULL WHERE id=$1 AND media_state<>'ready'`, id); e != nil {
			return e
		}
		if e = createFlowShort(ctx, tx, id, flow, now); e != nil {
			return e
		}
	default:
		return domain.ErrInvalid
	}
	return tx.Commit(ctx)
}
