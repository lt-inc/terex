package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

const recoveryMigration = `
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS error_code text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS failure_token text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS revalidation_attempted_at timestamptz;
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS revalidation_token text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS uploaded jsonb;
UPDATE twitch_collector.download_tasks SET failure_token=md5(clip_id||':'||updated_at::text||':'||epoch::text)
WHERE state='failed' AND failure_token='';
`

func (s *Store) SaveUploaded(ctx context.Context, t domain.Task, media domain.UploadedMedia) error {
	raw, err := json.Marshal(media)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET uploaded=$3 WHERE `+validLease, t.Clip.ID, t.Token, raw)
	if err == nil && tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	return err
}
func (s *Store) ClearUploaded(ctx context.Context, t domain.Task) error {
	tag, err := s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET uploaded=NULL WHERE `+validLease, t.Clip.ID, t.Token)
	if err == nil && tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	return err
}

func scanRecovery(row pgx.Row) (domain.FailedImport, error) {
	var out domain.FailedImport
	var raw, uploaded []byte
	err := row.Scan(&out.ClipID, &raw, &out.Reason, &out.FailedAt, &out.FailureToken, &out.ErrorCode, &out.FlowID, &out.FlowVersion, &uploaded)
	if err != nil {
		return out, translate(err)
	}
	if err = json.Unmarshal(raw, &out.Reservation); err != nil {
		return out, err
	}
	if len(uploaded) > 0 && string(uploaded) != "null" {
		err = json.Unmarshal(uploaded, &out.Uploaded)
	}
	return out, err
}

const recoveryColumns = `t.clip_id,COALESCE(t.reservation,'{}'::jsonb),t.last_error,t.updated_at,t.failure_token,t.error_code,f.id,COALESCE((f.config->>'version')::int,t.flow_version),t.uploaded`

func (s *Store) RecoverableImports(ctx context.Context, epoch int64) ([]domain.FailedImport, error) {
	// The only historical automatic backfill is the exact known version error,
	// and only if the saved version actually differs from the current flow.
	if _, err := s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET error_code='flow_version_changed'
FROM twitch_collector.flows f WHERE t.flow_id=f.id AND t.state='failed' AND t.epoch=$1 AND t.error_code=''
AND t.last_error='Terex request failed (HTTP 409): conflict: поток остановлен или его настройки изменились'
AND t.flow_version<>(f.config->>'version')::int AND COALESCE((t.reservation->>'upload_generation')::bigint,0)=0`, epoch); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+recoveryColumns+` FROM twitch_collector.download_tasks t JOIN LATERAL (
SELECT f.id,f.config FROM twitch_collector.consumers co JOIN twitch_collector.flows f ON f.id=co.flow_id
WHERE co.clip_id=t.clip_id AND co.eligible AND NOT co.excluded AND f.enabled ORDER BY (f.id=t.flow_id) DESC,f.id LIMIT 1) f ON TRUE
WHERE t.state='failed' AND t.epoch=$1 AND t.error_code='flow_version_changed'
AND (t.revalidation_attempted_at IS NULL OR t.revalidation_attempted_at<=now()-interval '1 minute')
ORDER BY t.revalidation_attempted_at NULLS FIRST,t.updated_at,t.clip_id LIMIT 20`, epoch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.FailedImport{}
	for rows.Next() {
		v, e := scanRecovery(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) FailedForRetry(ctx context.Context, id string) (domain.FailedImport, error) {
	return scanRecovery(s.pool.QueryRow(ctx, `SELECT `+recoveryColumns+` FROM twitch_collector.download_tasks t JOIN LATERAL (
SELECT choice.id,choice.config FROM (
SELECT f.id,f.config,0 AS priority FROM twitch_collector.consumers co JOIN twitch_collector.flows f ON f.id=co.flow_id WHERE co.clip_id=t.clip_id AND co.eligible AND NOT co.excluded AND f.enabled
UNION ALL SELECT f.id,f.config,1 AS priority FROM twitch_collector.flows f WHERE f.id=t.flow_id
)choice ORDER BY priority,(choice.id=t.flow_id) DESC,choice.id LIMIT 1)f ON TRUE WHERE t.clip_id=$1 AND t.state='failed'`, id))
}

func (s *Store) RevalidationAttempted(ctx context.Context, f domain.FailedImport) error {
	_, err := s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks SET revalidation_attempted_at=now() WHERE clip_id=$1 AND state='failed' AND failure_token=$2 AND epoch=$3`, f.ClipID, f.FailureToken, f.Reservation.Epoch)
	return err
}

func (s *Store) ApplyRevalidation(ctx context.Context, f domain.FailedImport, r twitch.RevalidationReceipt) error {
	if r.FailureToken != f.FailureToken || r.Epoch != f.Reservation.Epoch || r.AdmissionSequence != f.Reservation.AdmissionSequence || r.UploadGeneration != f.Reservation.UploadGeneration {
		return domain.ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(846923102)`); err != nil {
		return err
	}
	state, clipState := "discovered", "discovered"
	if r.State == "verification_retry" {
		state, clipState = "queued", "queued"
	} else if r.State == "ready" {
		state, clipState = "done", "imported"
	} else if r.State != "discovered" {
		return domain.ErrConflict
	}
	tag, err := tx.Exec(ctx, `UPDATE twitch_collector.download_tasks SET state=$5,attempts=0,run_at=now(),last_error='',error_code='',failure_synced_at=NULL,revalidation_token=$2,flow_version=$6,lease_token=NULL,lease_expires_at=NULL,
uploaded=CASE WHEN $5='queued' THEN uploaded ELSE NULL END,
reservation=CASE WHEN $5='discovered' THEN COALESCE(reservation,'{}'::jsonb)||jsonb_build_object('reservation_id','','upload_generation',0,'upload_import_id','','upload_url','','flow_id',$7::text,'flow_version',$6::int) ELSE reservation END,flow_id=$7
WHERE clip_id=$1 AND state='failed' AND failure_token=$2 AND epoch=$3 AND updated_at=$4 AND epoch=(SELECT epoch FROM twitch_collector.state WHERE id=1)`, f.ClipID, f.FailureToken, r.Epoch, f.FailedAt, state, r.FlowVersion, r.FlowID)
	if err != nil {
		return err
	}
	// A newer grant may have already reactivated the row using this receipt.
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_collector.clips SET state=$2,last_error='',error_kind='' WHERE clip_id=$1`, f.ClipID, clipState); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
