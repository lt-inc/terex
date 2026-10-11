package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"terex/proto/workflow"
)

// Every Twitch mutator takes lockTwitch first. Reset then fences admission and
// invalidates existing leases atomically with advancing the durable epoch.
func twitchResetEpoch(ctx context.Context, tx pgx.Tx) (int64, bool, error) {
	var epoch int64
	var active bool
	err := tx.QueryRow(ctx, `SELECT epoch,active FROM twitch_reset_control WHERE id=1`).Scan(&epoch, &active)
	return epoch, active, err
}
func twitchResetAllowed(ctx context.Context, tx pgx.Tx) error {
	_, active, err := twitchResetEpoch(ctx, tx)
	if err != nil {
		return err
	}
	if active {
		return waitPreparation("twitch_reset", "Сброс Twitch: новые задания временно остановлены", time.Now().UTC())
	}
	return nil
}
func twitchResetClipAllowed(ctx context.Context, tx pgx.Tx, clipID string) error {
	var managed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clips c JOIN jobs j ON j.job_id=c.job_id WHERE c.clip_id=$1 AND j.source_kind IN('twitch','twitch_compilation'))`, clipID).Scan(&managed); err != nil {
		return err
	}
	if managed {
		return twitchResetAllowed(ctx, tx)
	}
	return nil
}
func twitchResetRequestAllowed(ctx context.Context, tx pgx.Tx, epoch int64) error {
	current, active, err := twitchResetEpoch(ctx, tx)
	if err != nil {
		return err
	}
	if active || epoch != current {
		return waitPreparation("twitch_reset", "Сброс Twitch: устаревший запрос отклонён; обновите конфигурацию коллектора", time.Now().UTC())
	}
	return nil
}
func (s *Store) CheckTwitchEpoch(ctx context.Context, epoch int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	return twitchResetRequestAllowed(ctx, tx, epoch)
}

const resetJobsSQL = `SELECT job_id FROM jobs WHERE source_kind IN('twitch','twitch_compilation')`
const resetClipsSQL = `SELECT clip_id FROM clips WHERE job_id IN(` + resetJobsSQL + `)`

func resetBlockers(ctx context.Context, tx pgx.Tx) ([]twitch.ResetBlocker, error) {
	out := []twitch.ResetBlocker{}
	rows, err := tx.Query(ctx, `SELECT p.id,p.status FROM publications p WHERE p.clip_id IN(`+resetClipsSQL+`) AND (
 p.status IN('uploading','uploaded','processing','verifying','needs_attention') OR
 (p.started_at IS NOT NULL AND p.status NOT IN('published','cancelled')) OR
 EXISTS(SELECT 1 FROM publication_attempts a WHERE a.publication_id=p.id AND a.status='active' AND (a.lease_until>now() OR COALESCE(a.checkpoint->>'session_uri','')<>'' OR COALESCE((a.checkpoint->>'final_request_started')::boolean,false)))
) ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var state string
		if err = rows.Scan(&id, &state); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, twitch.ResetBlocker{Code: "publication_uncertain", PublicationID: id, Message: fmt.Sprintf("Публикация #%d имеет статус %s: сначала завершите или уточните результат загрузки YouTube. Удалённые видео сброс не удаляет.", id, state)})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var external bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_compilation_items ci JOIN clip_revisions source ON source.id=ci.source_revision_id JOIN clips sc ON sc.clip_id=source.clip_id JOIN jobs sj ON sj.job_id=sc.job_id JOIN clip_revisions target ON target.id=ci.compilation_revision_id JOIN clips tc ON tc.clip_id=target.clip_id JOIN jobs tj ON tj.job_id=tc.job_id WHERE sj.source_kind IN('twitch','twitch_compilation') AND tj.source_kind NOT IN('twitch','twitch_compilation'))`).Scan(&external)
	if external {
		out = append(out, twitch.ResetBlocker{Code: "external_history_dependency", Message: "Исходные Twitch-версии используются заданием вне Twitch. Удалите эту зависимость перед полным сбросом; чужие задания сохраняются."})
	}
	return out, err
}
func resetCounts(ctx context.Context, tx pgx.Tx) (c twitch.ResetCounts, err error) {
	err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM twitch_imports),(SELECT count(*) FROM clips WHERE job_id IN(`+resetJobsSQL+`)),(SELECT count(*) FROM clip_revisions WHERE clip_id IN(`+resetClipsSQL+`)),(SELECT count(*) FROM publications WHERE clip_id IN(`+resetClipsSQL+`)),(SELECT count(*) FROM jobs WHERE source_kind IN('twitch','twitch_compilation')),(SELECT count(*) FROM twitch_storage_objects WHERE present),(SELECT COALESCE(sum(size_bytes),0) FROM twitch_storage_objects WHERE present)`).Scan(&c.Imports, &c.Clips, &c.Revisions, &c.Publications, &c.Jobs, &c.Objects, &c.Bytes)
	return
}
func (s *Store) PreviewTwitchReset(ctx context.Context, now time.Time) (out twitch.ResetPreview, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return out, err
	}
	if err = twitchResetAllowed(ctx, tx); err != nil {
		return out, err
	}
	out.Counts, err = resetCounts(ctx, tx)
	if err != nil {
		return out, err
	}
	out.Blockers, err = resetBlockers(ctx, tx)
	if err != nil {
		return out, err
	}
	out.PreviewToken = uuid.NewString()
	out.ExpiresAt = now.Add(10 * time.Minute)
	out.ConfirmationPhrase = twitch.ResetConfirmation
	data, _ := json.Marshal(out.Counts)
	_, err = tx.Exec(ctx, `DELETE FROM twitch_reset_previews WHERE expires_at<$1 AND used_by IS NULL`, now)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO twitch_reset_previews(token,counts,expires_at)VALUES($1,$2,$3)`, out.PreviewToken, data, out.ExpiresAt)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *Store) StartTwitchReset(ctx context.Context, q twitch.ResetRequest, now time.Time) (out twitch.ResetStatus, err error) {
	if q.Confirmation != twitch.ResetConfirmation || q.PreviewToken == "" {
		return out, fmt.Errorf("%w: введите RESET TWITCH и сначала рассчитайте последствия сброса", domain.ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return out, err
	}
	var expires time.Time
	var used *string
	var data []byte
	err = tx.QueryRow(ctx, `SELECT expires_at,used_by,counts FROM twitch_reset_previews WHERE token=$1 FOR UPDATE`, q.PreviewToken).Scan(&expires, &used, &data)
	if err != nil {
		return out, mapErr(err)
	}
	if used != nil {
		if err = tx.Commit(ctx); err != nil {
			return out, err
		}
		return s.TwitchReset(ctx)
	}
	if !expires.After(now) {
		return out, fmt.Errorf("%w: предварительный расчёт устарел; повторите его", domain.ErrConflict)
	}
	if err = twitchResetAllowed(ctx, tx); err != nil {
		return out, err
	}
	blockers, err := resetBlockers(ctx, tx)
	if err != nil {
		return out, err
	}
	if len(blockers) > 0 {
		return out, fmt.Errorf("%w: %s", domain.ErrConflict, blockers[0].Message)
	}
	counts, err := resetCounts(ctx, tx)
	if err != nil {
		return out, err
	}
	data, _ = json.Marshal(counts)
	var epoch int64
	err = tx.QueryRow(ctx, `UPDATE twitch_reset_control SET epoch=epoch+1,active=true,reset_id=$1 WHERE id=1 RETURNING epoch`, uuid.NewString()).Scan(&epoch)
	if err != nil {
		return out, err
	}
	var id string
	err = tx.QueryRow(ctx, `SELECT reset_id FROM twitch_reset_control WHERE id=1`).Scan(&id)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO twitch_reset_runs(id,epoch,state,counts,created_at,updated_at)VALUES($1,$2,'stopping',$3,$4,$4)`, id, epoch, data, now)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `UPDATE twitch_reset_previews SET used_by=$2 WHERE token=$1`, q.PreviewToken, id)
	if err != nil {
		return out, err
	}
	// Changing versions makes stale collector flow snapshots invalid as well.
	_, err = tx.Exec(ctx, `UPDATE twitch_flows SET enabled=false,auto_publish=false,version=version+1,config=config||'{"enabled":false,"auto_publish":false}'::jsonb,updated_at=$1`, now)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `UPDATE render_attempts SET status='cancelled',lease_token=NULL WHERE revision_id IN(SELECT id FROM clip_revisions WHERE clip_id IN(`+resetClipsSQL+`)) AND status='active'`)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='cancelled',lease_token=NULL WHERE publication_id IN(SELECT id FROM publications WHERE clip_id IN(`+resetClipsSQL+`)) AND status='active'`)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `UPDATE publications SET auto_hold=true,cancel_requested=true,version=version+1,policy_hold_reason='twitch_reset' WHERE clip_id IN(`+resetClipsSQL+`) AND status NOT IN('published','cancelled')`)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return s.TwitchReset(ctx)
}

// Return only the durable fence, without aggregating deletion progress for the UI.
func (s *Store) TwitchCollectorReset(ctx context.Context) (int64, *twitch.ResetMaintenance, error) {
	var epoch int64
	var active bool
	var id, state string
	err := s.pool.QueryRow(ctx, `SELECT c.epoch,c.active,COALESCE(c.reset_id,''),COALESCE(r.state,'')
 FROM twitch_reset_control c LEFT JOIN twitch_reset_runs r ON r.id=c.reset_id WHERE c.id=1`).Scan(&epoch, &active, &id, &state)
	if err != nil {
		return 0, nil, err
	}
	if active {
		return epoch, &twitch.ResetMaintenance{ID: id, Epoch: epoch, State: state}, nil
	}
	return epoch, nil, nil
}

func (s *Store) TwitchReset(ctx context.Context) (out twitch.ResetStatus, err error) {
	var counts, blockers []byte
	err = s.pool.QueryRow(ctx, `SELECT id,state,epoch,collector_ack,gpu_drained,counts,blockers,error,created_at,updated_at,completed_at FROM twitch_reset_runs ORDER BY epoch DESC LIMIT 1`).Scan(&out.ID, &out.State, &out.Epoch, &out.CollectorAck, &out.GPUDrained, &counts, &blockers, &out.Error, &out.CreatedAt, &out.UpdatedAt, &out.CompletedAt)
	if err == pgx.ErrNoRows {
		out.State = "idle"
		out.Blockers = []twitch.ResetBlocker{}
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(counts, &out.Counts); err != nil {
		return out, err
	}
	if err = json.Unmarshal(blockers, &out.Blockers); err != nil {
		return out, err
	}
	err = s.pool.QueryRow(ctx, `SELECT count(*)FILTER(WHERE state='deleted'),COALESCE(sum(size_bytes)FILTER(WHERE state='deleted'),0),count(*)FILTER(WHERE state='pending'),count(*)FILTER(WHERE state='preserved') FROM twitch_reset_objects WHERE reset_id=$1`, out.ID).Scan(&out.DeletedObjects, &out.DeletedBytes, &out.RemainingObjects, &out.PreservedObjects)
	return out, err
}
func (s *Store) AcknowledgeTwitchReset(ctx context.Context, q twitch.ResetCollectorAck, now time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE twitch_reset_runs SET collector_ack=true,updated_at=$3 WHERE id=$1 AND epoch=$2`, q.ID, q.Epoch, now)
	if err == nil && tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return err
}
func (s *Store) AdvanceTwitchReset(ctx context.Context, id string, drained bool, now time.Time) (out twitch.ResetStatus, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return out, err
	}
	blockers, err := resetBlockers(ctx, tx)
	if err != nil {
		return out, err
	}
	data, _ := json.Marshal(blockers)
	_, err = tx.Exec(ctx, `UPDATE twitch_reset_runs SET gpu_drained=$2,blockers=$3,error='',state=CASE WHEN collector_ack AND $2 AND jsonb_array_length($3)=0 THEN 'deleting' ELSE 'stopping' END,updated_at=$4 WHERE id=$1 AND state IN('stopping','failed')`, id, drained, data, now)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return s.TwitchReset(ctx)
}

// A reset can ignore its own pins/retention, but never references held by other
// pipelines. All historical revisions count here, not just the current version.
const externalStorageRefSQL = `SELECT EXISTS(
 SELECT 1 FROM clips c JOIN jobs j ON j.job_id=c.job_id WHERE j.source_kind NOT IN('twitch','twitch_compilation') AND c.bucket=$1 AND c.object=$2
 UNION ALL SELECT 1 FROM clip_revisions r JOIN clips c ON c.clip_id=r.clip_id JOIN jobs j ON j.job_id=c.job_id WHERE j.source_kind NOT IN('twitch','twitch_compilation') AND ((r.bucket=$1 AND r.object=$2) OR (r.manifest->'original'->>'bucket'=$1 AND r.manifest->'original'->>'object'=$2) OR EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(r.manifest->'assembly'->'items','[]'))a WHERE a->'original'->>'bucket'=$1 AND a->'original'->>'object'=$2))
 UNION ALL SELECT 1 FROM banners WHERE bucket=$1 AND file_name=$2
 UNION ALL SELECT 1 FROM backgrounds WHERE bucket=$1 AND file_name=$2
 UNION ALL SELECT 1 FROM accounts WHERE bucket=$1 AND file_name=$2
)`

func (s *Store) QueueTwitchResetObjects(ctx context.Context, id string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	var ready bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_reset_runs r JOIN twitch_reset_control c ON c.reset_id=r.id WHERE r.id=$1 AND r.state='deleting' AND r.collector_ack AND r.gpu_drained AND c.active)`, id).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return domain.ErrConflict
	}
	rows, err := tx.Query(ctx, `SELECT bucket,object,size_bytes FROM twitch_storage_objects WHERE present`)
	if err != nil {
		return err
	}
	objects := []workflow.StorageObject{}
	for rows.Next() {
		var v workflow.StorageObject
		if err = rows.Scan(&v.Ref.Bucket, &v.Ref.Object, &v.SizeBytes); err != nil {
			rows.Close()
			return err
		}
		objects = append(objects, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range objects {
		var external bool
		if err = tx.QueryRow(ctx, externalStorageRefSQL, v.Ref.Bucket, v.Ref.Object).Scan(&external); err != nil {
			return err
		}
		state := "pending"
		if external {
			state = "preserved"
		}
		_, err = tx.Exec(ctx, `INSERT INTO twitch_reset_objects(reset_id,bucket,object,size_bytes,state)VALUES($1,$2,$3,$4,$5) ON CONFLICT(reset_id,bucket,object) DO UPDATE SET size_bytes=EXCLUDED.size_bytes,state=EXCLUDED.state`, id, v.Ref.Bucket, v.Ref.Object, v.SizeBytes, state)
		if err != nil {
			return err
		}
		if external {
			// A previous retention pass must not leave a shared object queued.
			if _, err = tx.Exec(ctx, `UPDATE twitch_storage_objects SET state='available' WHERE bucket=$1 AND object=$2`, v.Ref.Bucket, v.Ref.Object); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `DELETE FROM twitch_storage_deletions WHERE bucket=$1 AND object=$2`, v.Ref.Bucket, v.Ref.Object); err != nil {
				return err
			}
			continue
		}
		_, err = tx.Exec(ctx, `UPDATE twitch_storage_objects SET state='deleting' WHERE bucket=$1 AND object=$2`, v.Ref.Bucket, v.Ref.Object)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO twitch_storage_deletions(bucket,object,next_attempt_at)VALUES($1,$2,$3)ON CONFLICT(bucket,object)DO UPDATE SET state=CASE WHEN twitch_storage_deletions.state='deleted' THEN 'pending' ELSE twitch_storage_deletions.state END,next_attempt_at=CASE WHEN twitch_storage_deletions.state='deleted' THEN $3 ELSE twitch_storage_deletions.next_attempt_at END`, v.Ref.Bucket, v.Ref.Object, now)
		if err != nil {
			return err
		}
		if err = markStorageUnavailable(ctx, tx, v.Ref, "deleting", now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) PendingTwitchResetObjects(ctx context.Context, id string, limit int) (out []workflow.StorageObject, err error) {
	rows, err := s.pool.Query(ctx, `SELECT o.bucket,o.object,o.size_bytes FROM twitch_reset_objects o JOIN twitch_storage_deletions d USING(bucket,object) WHERE o.reset_id=$1 AND o.state='pending' AND d.next_attempt_at<=now() ORDER BY o.bucket,o.object LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out = []workflow.StorageObject{}
	for rows.Next() {
		var v workflow.StorageObject
		if err = rows.Scan(&v.Ref.Bucket, &v.Ref.Object, &v.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) CheckTwitchResetObject(ctx context.Context, id string, ref workflow.ObjectRef) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return false, err
	}
	var allowed, external bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_reset_runs r JOIN twitch_reset_control c ON c.reset_id=r.id JOIN twitch_reset_objects o ON o.reset_id=r.id WHERE r.id=$1 AND c.active AND r.state='deleting' AND r.collector_ack AND r.gpu_drained AND o.bucket=$2 AND o.object=$3 AND o.state='pending')`, id, ref.Bucket, ref.Object).Scan(&allowed)
	if err != nil || !allowed {
		return false, err
	}
	if err = tx.QueryRow(ctx, externalStorageRefSQL, ref.Bucket, ref.Object).Scan(&external); err != nil {
		return false, err
	}
	if external {
		_, err = tx.Exec(ctx, `UPDATE twitch_reset_objects SET state='preserved' WHERE reset_id=$1 AND bucket=$2 AND object=$3`, id, ref.Bucket, ref.Object)
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `UPDATE twitch_storage_objects SET state='available' WHERE bucket=$1 AND object=$2`, ref.Bucket, ref.Object)
		if err != nil {
			return false, err
		}
	}
	return !external, tx.Commit(ctx)
}
func (s *Store) FinishTwitchResetObject(ctx context.Context, id string, ref workflow.ObjectRef, failure error, now time.Time) error {
	// Reuse the normal durable queue's retry and tombstone state; a reset never
	// clears metadata until every non-shared physical object has been confirmed.
	if err := s.FinishTwitchDeletion(ctx, ref, failure, now); err != nil {
		return err
	}
	if failure != nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE twitch_reset_objects SET state='deleted' WHERE reset_id=$1 AND bucket=$2 AND object=$3`, id, ref.Bucket, ref.Object)
	return err
}
func (s *Store) FailTwitchReset(ctx context.Context, id, message string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE twitch_reset_runs SET state='failed',error=$2,updated_at=$3 WHERE id=$1 AND state<>'completed'`, id, message, now)
	return err
}
func (s *Store) ClearTwitchReset(ctx context.Context, id string, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	var ready bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_reset_runs r JOIN twitch_reset_control c ON c.reset_id=r.id WHERE r.id=$1 AND c.active AND r.collector_ack AND r.gpu_drained AND r.state IN('deleting','clearing') AND NOT EXISTS(SELECT 1 FROM twitch_reset_objects WHERE reset_id=r.id AND state='pending'))`, id).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return domain.ErrConflict
	}
	blockers, err := resetBlockers(ctx, tx)
	if err != nil {
		return err
	}
	if len(blockers) > 0 {
		return fmt.Errorf("%w: %s", domain.ErrConflict, blockers[0].Message)
	}
	var phase string
	if err = tx.QueryRow(ctx, `SELECT state FROM twitch_reset_runs WHERE id=$1`, id).Scan(&phase); err != nil {
		return err
	}
	if phase == "deleting" {
		if _, err = tx.Exec(ctx, `UPDATE twitch_reset_runs SET state='clearing',updated_at=$2 WHERE id=$1`, id, now); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	queries := []string{
		`UPDATE twitch_reset_runs SET state='clearing' WHERE id=$1`,
		`DELETE FROM twitch_monitoring_snapshots`,
		`DELETE FROM twitch_view_samples`,
		`DELETE FROM twitch_storage_pins`,
		`DELETE FROM twitch_storage_references`,
		`DELETE FROM twitch_source_uses`,
		`DELETE FROM twitch_compilation_items WHERE compilation_revision_id IN(SELECT id FROM clip_revisions WHERE clip_id IN(` + resetClipsSQL + `))`,
		`DELETE FROM twitch_preparation_sources`,
		`DELETE FROM twitch_preparation_reservations`,
		`DELETE FROM twitch_outputs`,
		`DELETE FROM twitch_flow_sources`,
		`DELETE FROM twitch_import_keys`,
		`DELETE FROM twitch_download_reservations`,
		`DELETE FROM twitch_imports`,
		`DELETE FROM twitch_media`,
		`DELETE FROM publication_outbox WHERE publication_id IN(SELECT id FROM publications WHERE clip_id IN(` + resetClipsSQL + `)) OR (kind='render_revision' AND (payload->>'revision_id')::bigint IN(SELECT id FROM clip_revisions WHERE clip_id IN(` + resetClipsSQL + `)))`,
		`DELETE FROM jobs WHERE source_kind IN('twitch','twitch_compilation')`,
		`UPDATE twitch_flows SET enabled=false,auto_publish=false,work_started_at=NULL,last_checked_at=NULL,config=config||'{"enabled":false,"auto_publish":false}'::jsonb`,
	}
	for n, q := range queries {
		if n == 0 {
			_, err = tx.Exec(ctx, q, id)
		} else {
			_, err = tx.Exec(ctx, q)
		}
		if err != nil {
			return fmt.Errorf("clear Twitch reset step %d: %w", n, err)
		}
	}
	_, err = tx.Exec(ctx, `UPDATE twitch_reset_runs SET state='completed',error='',updated_at=$2,completed_at=$2 WHERE id=$1`, id, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE twitch_reset_control SET active=false WHERE id=1 AND reset_id=$1`, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
