package postgres

import (
	"context"
	"encoding/json"
	"fmt"
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
	const base = ` FROM twitch_flow_sources fs JOIN twitch_imports i ON i.id=fs.import_id
 WHERE fs.flow_id=$1 AND($2='' OR i.request->'source'->>'broadcaster_id'=$2)
 AND($3='' OR concat(i.request->'source'->>'title',' ',i.request->'source'->>'broadcaster_login',' ',i.twitch_clip_id) ILIKE '%'||$3||'%')
 AND($4='' OR EXISTS(SELECT 1 FROM twitch_flows fl WHERE fl.id=fs.flow_id AND COALESCE((fl.config->>($4||'s_enabled'))::boolean,false)) OR EXISTS(SELECT 1 FROM twitch_outputs o WHERE o.flow_id=fs.flow_id AND o.format=$4 AND(o.source_id=i.id OR o.clip_id IN(SELECT r.clip_id FROM twitch_compilation_items ci JOIN clip_revisions r ON r.id=ci.compilation_revision_id WHERE ci.source_id=i.id))))
 AND($5='' OR $5=CASE WHEN fs.excluded OR i.rejected THEN 'excluded' WHEN i.media_state='ready' AND EXISTS(SELECT 1 FROM twitch_source_uses u WHERE u.youtube_channel_id=fs.config->>'youtube_channel_id' AND u.media_id=i.media_id AND($4='' OR u.format=$4)) THEN 'used' WHEN i.media_state='ready' THEN 'ready' WHEN i.state='failed' THEN 'failed' WHEN i.state='upload_pending' THEN 'downloading' ELSE 'discovered' END
 OR(NOT fs.excluded AND NOT i.rejected AND i.media_state<>'ready' AND i.twitch_clip_id=ANY($6::text[]))
 OR($5='failed' AND EXISTS(SELECT 1 FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id LEFT JOIN publications p ON p.clip_id=o.clip_id WHERE o.flow_id=fs.flow_id AND($4='' OR o.format=$4) AND(o.source_id=i.id OR EXISTS(SELECT 1 FROM twitch_compilation_items ci WHERE ci.compilation_revision_id=r.id AND ci.source_id=i.id)) AND(r.render_state='failed' OR p.status IN('failed','blocked','needs_attention')))))`
	args := []any{id, q.BroadcasterID, q.Q, q.Format, q.State, q.CollectorSourceIDs}
	if e := s.pool.QueryRow(ctx, `SELECT count(*)`+base, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	args = append(args, out.Limit, out.Offset)
	rows, e := s.pool.Query(ctx, `SELECT i.id,i.request,fs.excluded,i.rejected,COALESCE(i.media_id,''),i.media_state,i.state,COALESCE(i.error,''),i.upload_claim_until`+base+` ORDER BY fs.created_at DESC,i.id LIMIT $7 OFFSET $8`, args...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var row domain.FlowClip
		var req []byte
		var mediaState, state, errorMsg string
		var until *time.Time
		if e = rows.Scan(&row.ImportID, &req, &row.Excluded, &row.Rejected, &row.MediaID, &mediaState, &state, &errorMsg, &until); e != nil {
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
		switch {
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
		if !row.Rejected && !row.Excluded && len(row.Outputs) == 0 && !sourceEligible(f, row.Source, time.Now()) {
			row.SourceState = "filtered"
			row.Reason.Code = "filtered"
			row.Reason.Message = sourceFilterMessage(f, row.Source, time.Now())
			row.Reason.Actions = []string{"exclude"}
		}
		if row.SourceState == "ready" && len(row.Outputs) > 0 {
			row.SourceState = "used"
			row.Reason.Code = "output_created"
			row.Reason.Message = "Ролик подготовлен из этого исходника"
		}
	}
	return out, nil
}
func sourceFilterMessage(f twitch.Flow, src twitch.Source, now time.Time) string {
	switch {
	case !sourceMatches(f, src):
		return "Исходник не соответствует каналам, категории или языку потока"
	case src.ViewCount < f.MinViews:
		return fmt.Sprintf("%d просмотров; требуется минимум %d", src.ViewCount, f.MinViews)
	case src.CreatedAt.After(now.Add(-time.Duration(f.MinAgeMinutes) * time.Minute)):
		return fmt.Sprintf("Исходник слишком новый; минимальный возраст %d мин", f.MinAgeMinutes)
	case src.CreatedAt.Before(now.Add(-time.Duration(f.WindowHours) * time.Hour)):
		return fmt.Sprintf("Исходник вне окна последних %d часов", f.WindowHours)
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
