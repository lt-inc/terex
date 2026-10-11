package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/workflow"
	"time"
)

// Rebuild references from authoritative immutable records, including restored
// revisions which can share the exact same rendered object. History records are
// retained, but only live work, pins and recent publication extend retention.
const storageReferenceSQL = `
WITH managed AS (
 SELECT c.clip_id,o.source_id FROM clips c JOIN jobs j ON j.job_id=c.job_id
 LEFT JOIN twitch_outputs o ON o.clip_id=c.clip_id WHERE j.source_kind IN('twitch','twitch_compilation')
), refs AS (
 SELECT m.verified->'original'->>'bucket' bucket,m.verified->'original'->>'object' object,
 'import/'||i.id ref_key,i.id import_id,NULL::text clip_id,NULL::bigint revision_id,
 COALESCE(i.verified_at,m.created_at) imported_at
 FROM twitch_imports i JOIN twitch_media m ON m.id=i.media_id
 UNION ALL
 SELECT r.bucket,r.object,'render/'||r.id,NULL,o.clip_id,r.id,r.created_at
 FROM managed o JOIN clip_revisions r ON r.clip_id=o.clip_id WHERE COALESCE(r.object,'')<>''
 UNION ALL
 SELECT r.manifest->'original'->>'bucket',r.manifest->'original'->>'object','original/'||r.id,o.source_id,o.clip_id,r.id,r.created_at
 FROM managed o JOIN clip_revisions r ON r.clip_id=o.clip_id
 WHERE COALESCE(r.manifest->'original'->>'object','')<>''
 UNION ALL
 SELECT item->'original'->>'bucket',item->'original'->>'object','assembly/'||r.id||'/'||ord,
 COALESCE(NULLIF(item->>'twitch_source_id',''),i.id),o.clip_id,r.id,r.created_at
 FROM managed o JOIN clip_revisions r ON r.clip_id=o.clip_id
 CROSS JOIN LATERAL jsonb_array_elements(COALESCE(r.manifest->'assembly'->'items','[]')) WITH ORDINALITY a(item,ord)
 LEFT JOIN twitch_imports i ON i.clip_id=item->>'source_clip_id'
 UNION ALL
 SELECT i.original->>'bucket',i.original->>'object','staging/'||i.id,i.id,NULL,NULL,i.created_at
 FROM twitch_imports i WHERE i.original->>'bucket'='imports' AND i.original->>'object' LIKE 'staging/%'
)
INSERT INTO twitch_storage_references(bucket,object,ref_key,import_id,clip_id,revision_id)
SELECT bucket,object,ref_key,import_id,clip_id,revision_id FROM refs
WHERE COALESCE(bucket,'')<>'' AND COALESCE(object,'')<>'' ON CONFLICT DO NOTHING`

func refreshStorageReferences(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DELETE FROM twitch_storage_references`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, storageReferenceSQL)
	return err
}
func storageKind(ref workflow.ObjectRef) string {
	if ref.Bucket == "clips" {
		return "render"
	}
	if strings.HasPrefix(ref.Object, "staging/") {
		return "staging"
	}
	if strings.HasPrefix(ref.Object, "assemblies/") {
		return "assembly"
	}
	return "source"
}
func (s *Store) SyncTwitchStorage(ctx context.Context, objects []workflow.StorageObject, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_storage_objects SET present=FALSE`); err != nil {
		return err
	}
	for _, v := range objects {
		if v.ModifiedAt.IsZero() {
			return fmt.Errorf("Twitch inventory object has no modification time")
		}
		_, err = tx.Exec(ctx, `INSERT INTO twitch_storage_objects(bucket,object,kind,size_bytes,imported_at,modified_at,last_seen_at,present)
VALUES($1,$2,$3,$4,$5,$5,$6,TRUE) ON CONFLICT(bucket,object) DO UPDATE SET size_bytes=EXCLUDED.size_bytes,modified_at=EXCLUDED.modified_at,last_seen_at=EXCLUDED.last_seen_at,present=TRUE,state=CASE WHEN twitch_storage_objects.state='deleted' THEN 'deleting' ELSE twitch_storage_objects.state END`, v.Ref.Bucket, v.Ref.Object, storageKind(v.Ref), v.SizeBytes, v.ModifiedAt, now)
		if err != nil {
			return err
		}
	}
	// A stale presigned PUT or a late fenced worker can recreate bytes after a
	// successful delete. The tombstone remains unavailable and the durable queue
	// removes those bytes again instead of resurrecting an expired source.
	if _, err = tx.Exec(ctx, `UPDATE twitch_storage_deletions d SET state='pending',next_attempt_at=$1,error=NULL
FROM twitch_storage_objects o WHERE o.bucket=d.bucket AND o.object=d.object AND o.present AND o.state='deleting' AND d.state='deleted'`, now); err != nil {
		return err
	}
	if err = refreshStorageReferences(ctx, tx); err != nil {
		return err
	}
	// Verification time, not discovery time or object upload time, starts source retention.
	if _, err = tx.Exec(ctx, `UPDATE twitch_storage_objects o SET imported_at=x.verified_at FROM (
SELECT m.verified->'original'->>'bucket' bucket,m.verified->'original'->>'object' object,max(COALESCE(i.verified_at,m.created_at)) verified_at
FROM twitch_imports i JOIN twitch_media m ON m.id=i.media_id GROUP BY 1,2)x
WHERE o.bucket=x.bucket AND o.object=x.object AND o.kind='source'`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// All protection is evaluated per physical object, across every flow/revision.
// An on-hold draft and historical reference alone do not hold storage forever.
const storageFlagsSQL = `WITH flags AS (
SELECT o.*,
ARRAY(SELECT DISTINCT r.clip_id FROM twitch_storage_references r WHERE r.bucket=o.bucket AND r.object=o.object AND r.clip_id IS NOT NULL) clip_ids,
ARRAY(SELECT DISTINCT r.import_id FROM twitch_storage_references r WHERE r.bucket=o.bucket AND r.object=o.object AND r.import_id IS NOT NULL) import_ids,
EXISTS(SELECT 1 FROM twitch_storage_references r JOIN twitch_storage_pins p ON
 (p.kind='clip' AND p.target_id=r.clip_id AND EXISTS(SELECT 1 FROM clip_revisions pinned_revision WHERE pinned_revision.id=r.revision_id AND pinned_revision.storage_state='available')) OR(p.kind='import' AND p.target_id=r.import_id)
 WHERE r.bucket=o.bucket AND r.object=o.object AND o.kind<>'staging') kept,
COALESCE((SELECT jsonb_agg(DISTINCT jsonb_build_object('kind',p.kind,'id',p.target_id))
 FROM twitch_storage_references r JOIN twitch_storage_pins p ON
 (p.kind='clip' AND p.target_id=r.clip_id AND EXISTS(SELECT 1 FROM clip_revisions pinned_revision WHERE pinned_revision.id=r.revision_id AND pinned_revision.storage_state='available')) OR(p.kind='import' AND p.target_id=r.import_id)
 WHERE r.bucket=o.bucket AND r.object=o.object AND o.kind<>'staging'),'[]'::jsonb) pins,
CASE
 WHEN o.kind='staging' AND o.accepted_staging THEN o.imported_at
 WHEN o.kind='staging' OR NOT EXISTS(SELECT 1 FROM twitch_storage_references r WHERE r.bucket=o.bucket AND r.object=o.object) THEN o.modified_at+interval '48 hours'
 ELSE GREATEST(o.imported_at,COALESCE((SELECT max(COALESCE(p.published_at,p.uploaded_at,p.publish_at))
 FROM twitch_storage_references r JOIN publications p ON p.revision_id=r.revision_id
 WHERE r.bucket=o.bucket AND r.object=o.object AND p.status='published'),o.imported_at))+interval '7 days' END expires_at,
CASE
 WHEN EXISTS(SELECT 1 FROM twitch_storage_references r JOIN twitch_storage_pins p ON
 (p.kind='clip' AND p.target_id=r.clip_id AND EXISTS(SELECT 1 FROM clip_revisions pinned_revision WHERE pinned_revision.id=r.revision_id AND pinned_revision.storage_state='available')) OR(p.kind='import' AND p.target_id=r.import_id)
 WHERE r.bucket=o.bucket AND r.object=o.object AND o.kind<>'staging') THEN 'Сохранено оператором'
 WHEN EXISTS(SELECT 1 FROM twitch_storage_references r JOIN publications p ON p.revision_id=r.revision_id
 WHERE r.bucket=o.bucket AND r.object=o.object AND (p.status IN('scheduled','queued','retry_wait','uploading','uploaded','processing','verifying','needs_attention')
 OR(p.started_at IS NOT NULL AND p.status NOT IN('published','cancelled')))) THEN 'Запланированный выпуск или незавершённая публикация'
 WHEN EXISTS(SELECT 1 FROM twitch_storage_references r JOIN render_attempts a ON a.revision_id=r.revision_id
 WHERE r.bucket=o.bucket AND r.object=o.object AND a.status='active' AND ((a.execution_started_at IS NOT NULL AND a.lease_until>$1) OR (a.execution_started_at IS NULL AND a.dispatch_deadline>$1))) OR
 EXISTS(SELECT 1 FROM render_attempts a JOIN clip_revisions rv ON rv.id=a.revision_id JOIN clips c ON c.clip_id=rv.clip_id JOIN jobs j ON j.job_id=c.job_id
 WHERE j.source_kind IN('twitch','twitch_compilation') AND a.status='active' AND ((a.execution_started_at IS NOT NULL AND a.lease_until>$1) OR (a.execution_started_at IS NULL AND a.dispatch_deadline>$1)) AND (o.object LIKE '%'||a.id||'.mp4' OR o.object LIKE '%'||a.id||'.mp4.original.mp4')) THEN 'Принятая очередь рендера или активный рендер'
 WHEN EXISTS(SELECT 1 FROM twitch_storage_references r JOIN twitch_imports i ON i.id=r.import_id
 WHERE r.bucket=o.bucket AND r.object=o.object AND(i.upload_claim_until>$1 OR i.upload_expires_at>$1) AND i.media_state<>'ready') THEN 'Активная загрузка или проверка файла'
 WHEN EXISTS(SELECT 1 FROM twitch_storage_references r JOIN twitch_preparation_sources ps ON ps.import_id=r.import_id
 JOIN twitch_preparation_reservations pr ON pr.id=ps.reservation_id JOIN twitch_flows f ON f.id=pr.flow_id
 WHERE r.bucket=o.bucket AND r.object=o.object AND pr.state='active' AND pr.expires_at>$1 AND f.enabled AND NOT f.archived)
 OR EXISTS(SELECT 1 FROM twitch_storage_references r JOIN twitch_preparation_reservations pr ON pr.clip_id=r.clip_id JOIN twitch_flows f ON f.id=pr.flow_id
 WHERE r.bucket=o.bucket AND r.object=o.object AND pr.state='active' AND pr.expires_at>$1 AND f.enabled AND NOT f.archived) THEN 'Действующий резерв подготовки'
 ELSE '' END protected_reason
FROM twitch_storage_objects o
)
`

func storageItems(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, now time.Time) ([]domain.TwitchStorageItem, error) {
	rows, err := q.Query(ctx, storageFlagsSQL+`SELECT f.bucket,f.object,f.kind,f.size_bytes,f.state,f.kept,f.pins,f.expires_at,f.protected_reason,f.clip_ids,f.import_ids,COALESCE(d.error,'')
FROM flags f LEFT JOIN twitch_storage_deletions d ON d.bucket=f.bucket AND d.object=f.object WHERE f.present OR f.state='deleted' ORDER BY f.expires_at,f.bucket,f.object`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TwitchStorageItem{}
	for rows.Next() {
		var v domain.TwitchStorageItem
		if err = rows.Scan(&v.Bucket, &v.Object, &v.Kind, &v.SizeBytes, &v.State, &v.Keep, &v.Pins, &v.ExpiresAt, &v.ProtectedReason, &v.ClipIDs, &v.ImportIDs, &v.Error); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) TwitchStorage(ctx context.Context, now time.Time) (domain.TwitchStorageStats, error) {
	out := domain.TwitchStorageStats{}
	items, err := storageItems(ctx, s.pool, now)
	if err != nil {
		return out, err
	}
	out.Items = items
	for _, v := range items {
		if v.State == "deleted" {
			out.DeletedObjects++
			continue
		}
		out.Objects++
		out.PhysicalBytes += v.SizeBytes
		if v.ProtectedReason != "" {
			out.ProtectedObjects++
		} else if !v.ExpiresAt.After(now) {
			out.ReclaimableBytes += v.SizeBytes
		}
	}
	err = s.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='failed') FROM twitch_storage_deletions`).Scan(&out.PendingDeletions, &out.FailedDeletions)
	if err == nil {
		out.Pressure, err = s.twitchStoragePressure(ctx)
	}
	return out, err
}

func markStorageUnavailable(ctx context.Context, tx pgx.Tx, ref workflow.ObjectRef, state string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE twitch_media SET storage_state=$3 WHERE verified->'original'->>'bucket'=$1 AND verified->'original'->>'object'=$2`, ref.Bucket, ref.Object, state); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE twitch_imports i SET media_state='expired',state='expired',error='Файл удалён по сроку хранения',updated_at=$3
FROM twitch_media m WHERE i.media_id=m.id AND m.verified->'original'->>'bucket'=$1 AND m.verified->'original'->>'object'=$2`, ref.Bucket, ref.Object, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE clip_revisions SET storage_state=$3,render_state='expired',error='Файл удалён по сроку хранения',approved_at=NULL
WHERE id IN(SELECT revision_id FROM twitch_storage_references WHERE bucket=$1 AND object=$2 AND revision_id IS NOT NULL)`, ref.Bucket, ref.Object, state)
	return err
}
func (s *Store) QueueTwitchCleanup(ctx context.Context, now time.Time, limit int) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return 0, err
	}
	_, resetActive, e := twitchResetEpoch(ctx, tx)
	if e != nil {
		return 0, e
	}
	if resetActive {
		return 0, nil
	}
	if err = refreshStorageReferences(ctx, tx); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, storageFlagsSQL+`SELECT bucket,object FROM flags WHERE present AND state='available' AND protected_reason='' AND expires_at<=$1 ORDER BY expires_at LIMIT $2`, now, limit)
	if err != nil {
		return 0, err
	}
	refs := []workflow.ObjectRef{}
	for rows.Next() {
		var ref workflow.ObjectRef
		if err = rows.Scan(&ref.Bucket, &ref.Object); err != nil {
			rows.Close()
			return 0, err
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	queued := 0
	for _, ref := range refs {
		var external bool
		if err = tx.QueryRow(ctx, externalStorageRefSQL, ref.Bucket, ref.Object).Scan(&external); err != nil {
			return 0, err
		}
		if external {
			continue
		}
		if _, err = tx.Exec(ctx, `UPDATE twitch_storage_objects SET state='deleting' WHERE bucket=$1 AND object=$2`, ref.Bucket, ref.Object); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_storage_deletions(bucket,object,next_attempt_at)VALUES($1,$2,$3)ON CONFLICT DO NOTHING`, ref.Bucket, ref.Object, now); err != nil {
			return 0, err
		}
		if err = markStorageUnavailable(ctx, tx, ref, "deleting", now); err != nil {
			return 0, err
		}
		queued++
	}
	return queued, tx.Commit(ctx)
}
func (s *Store) PendingTwitchDeletion(ctx context.Context, now time.Time, limit int) ([]workflow.ObjectRef, error) {
	rows, err := s.pool.Query(ctx, `SELECT bucket,object FROM twitch_storage_deletions WHERE state IN('pending','failed') AND next_attempt_at<=$1 ORDER BY created_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflow.ObjectRef{}
	for rows.Next() {
		var ref workflow.ObjectRef
		if err = rows.Scan(&ref.Bucket, &ref.Object); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
func (s *Store) CheckTwitchDeletion(ctx context.Context, ref workflow.ObjectRef, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return false, err
	}
	_, resetActive, e := twitchResetEpoch(ctx, tx)
	if e != nil {
		return false, e
	}
	if resetActive {
		return false, nil
	}
	var external bool
	if err = tx.QueryRow(ctx, externalStorageRefSQL, ref.Bucket, ref.Object).Scan(&external); err != nil {
		return false, err
	}
	if external {
		return false, nil
	}
	if err = refreshStorageReferences(ctx, tx); err != nil {
		return false, err
	}
	var allowed bool
	err = tx.QueryRow(ctx, storageFlagsSQL+`SELECT state='deleting' AND protected_reason='' AND expires_at<=$1 FROM flags WHERE bucket=$2 AND object=$3`, now, ref.Bucket, ref.Object).Scan(&allowed)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return allowed, tx.Commit(ctx)
}
func (s *Store) FinishTwitchDeletion(ctx context.Context, ref workflow.ObjectRef, failure error, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if failure != nil {
		_, err = tx.Exec(ctx, `UPDATE twitch_storage_deletions SET state='failed',attempts=attempts+1,error=$3,next_attempt_at=$4 WHERE bucket=$1 AND object=$2`, ref.Bucket, ref.Object, failure.Error(), now.Add(30*time.Minute))
	} else {
		_, err = tx.Exec(ctx, `UPDATE twitch_storage_deletions SET state='deleted',attempts=attempts+1,error=NULL,deleted_at=$3 WHERE bucket=$1 AND object=$2`, ref.Bucket, ref.Object, now)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE twitch_storage_objects SET state='deleted',present=FALSE,deleted_at=$3 WHERE bucket=$1 AND object=$2`, ref.Bucket, ref.Object, now)
		}
		if err == nil {
			err = markStorageUnavailable(ctx, tx, ref, "deleted", now)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) KeepTwitchStorage(ctx context.Context, kind, id string, keep bool) error {
	if kind != "import" && kind != "clip" {
		return domain.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	var found bool
	if kind == "import" {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_imports WHERE id=$1)`, id).Scan(&found)
	} else {
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clips c JOIN jobs j ON j.job_id=c.job_id WHERE c.clip_id=$1 AND j.source_kind IN('twitch','twitch_compilation'))`, id).Scan(&found)
	}
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrNotFound
	}
	if keep {
		if err = refreshStorageReferences(ctx, tx); err != nil {
			return err
		}
		var unavailable bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_storage_references r JOIN twitch_storage_objects o USING(bucket,object) WHERE (($1='clip' AND r.clip_id=$2 AND r.revision_id=(SELECT latest_revision_id FROM clips WHERE clip_id=$2)) OR($1='import' AND r.import_id=$2)) AND o.state<>'available')`, kind, id).Scan(&unavailable)
		if err != nil {
			return err
		}
		if unavailable {
			return fmt.Errorf("%w: файл уже удалён или ожидает удаления", domain.ErrConflict)
		}
		_, err = tx.Exec(ctx, `INSERT INTO twitch_storage_pins(kind,target_id)VALUES($1,$2)ON CONFLICT DO NOTHING`, kind, id)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM twitch_storage_pins WHERE kind=$1 AND target_id=$2`, kind, id)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Accepted staging references are registered inside the same transaction which
// accepts verified media. A failed DB commit therefore never deletes the upload.
func registerAcceptedStagingTx(ctx context.Context, tx pgx.Tx, ref workflow.ObjectRef, now time.Time) error {
	if ref.Bucket != "imports" || !strings.HasPrefix(ref.Object, "staging/") {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO twitch_storage_objects(bucket,object,kind,imported_at,modified_at,last_seen_at,accepted_staging)
VALUES($1,$2,'staging',$3,$3,$3,TRUE)ON CONFLICT(bucket,object)DO UPDATE SET accepted_staging=TRUE`, ref.Bucket, ref.Object, now)
	return err
}

// Callers acquire lockTwitch before this check and before binding new media.
func storageRefsAvailable(ctx context.Context, tx pgx.Tx, refs ...workflow.ObjectRef) error {
	for _, ref := range refs {
		if ref.Object == "" {
			continue
		}
		var unavailable bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_storage_objects WHERE bucket=$1 AND object=$2 AND state<>'available')`, ref.Bucket, ref.Object).Scan(&unavailable); err != nil {
			return err
		}
		if unavailable {
			return fmt.Errorf("%w: файл удалён по сроку хранения или ожидает удаления", domain.ErrConflict)
		}
	}
	return nil
}
func storageClipAvailable(ctx context.Context, tx pgx.Tx, clipID string) error {
	var unavailable bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM clip_revisions r JOIN clips c ON c.clip_id=r.clip_id JOIN jobs j ON j.job_id=c.job_id WHERE r.clip_id=$1 AND j.source_kind IN('twitch','twitch_compilation') AND r.id=c.latest_revision_id AND storage_state<>'available')`, clipID).Scan(&unavailable)
	if err != nil {
		return err
	}
	if unavailable {
		return fmt.Errorf("%w: файл удалён по сроку хранения", domain.ErrConflict)
	}
	return nil
}

func storageRevisionAvailable(ctx context.Context, tx pgx.Tx, revisionID int64) error {
	var state, bucket, object string
	var data []byte
	if err := tx.QueryRow(ctx, `SELECT storage_state,COALESCE(bucket,''),COALESCE(object,''),manifest FROM clip_revisions WHERE id=$1`, revisionID).Scan(&state, &bucket, &object, &data); err != nil {
		return mapErr(err)
	}
	if state != "available" {
		return fmt.Errorf("%w: файл удалён по сроку хранения", domain.ErrConflict)
	}
	var manifest workflow.SourceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	refs := []workflow.ObjectRef{{Bucket: bucket, Object: object}, manifest.Original}
	if manifest.Assembly != nil {
		for _, item := range manifest.Assembly.Items {
			refs = append(refs, item.Original)
		}
	}
	return storageRefsAvailable(ctx, tx, refs...)
}
