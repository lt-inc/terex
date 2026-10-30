package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
)

const revisionCols = `id,clip_id,revision_no,spec,render_state,COALESCE(bucket,''),COALESCE(object,''),manifest,approved_at,error,created_at`

func scanRevision(row pgx.Row) (domain.Revision, error) {
	var r domain.Revision
	var spec []byte
	err := row.Scan(&r.ID, &r.ClipID, &r.Version, &spec, &r.RenderState, &r.Bucket, &r.Object, &r.Manifest, &r.ApprovedAt, &r.Error, &r.CreatedAt)
	if err == nil {
		err = json.Unmarshal(spec, &r.EditSpec)
	}
	return r, mapErr(err)
}

func (s *Store) InitializeRevision(ctx context.Context, clipID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,bucket,object)
      SELECT clip_id,1,jsonb_build_object('start_sec',start_sec,'end_sec',end_sec,'title',title,'description','','hashtags',hashtags,'cues','[]'::jsonb),bucket,object FROM clips WHERE clip_id=$1
      ON CONFLICT(clip_id,revision_no) WHERE NOT publication_only DO UPDATE SET clip_id=EXCLUDED.clip_id RETURNING id`, clipID).Scan(&id)
	if err != nil {
		return mapErr(err)
	}
	_, err = tx.Exec(ctx, `UPDATE clips SET latest_revision_id=COALESCE(latest_revision_id,$2) WHERE clip_id=$1`, clipID, id)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) LatestRevision(ctx context.Context, clipID string) (domain.Revision, error) {
	return scanRevision(s.pool.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=(SELECT latest_revision_id FROM clips WHERE clip_id=$1)`, clipID))
}
func (s *Store) RevisionByID(ctx context.Context, id int64) (domain.Revision, error) {
	return scanRevision(s.pool.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, id))
}
func (s *Store) SetRevisionManifest(ctx context.Context, id int64, m json.RawMessage) error {
	_, err := s.pool.Exec(ctx, `UPDATE clip_revisions SET manifest=$2,
      spec=CASE WHEN revision_no=1 AND approved_at IS NULL AND jsonb_array_length(COALESCE(spec->'cues','[]'::jsonb))=0 THEN
      spec||jsonb_build_object('cues',COALESCE($2::jsonb->'cues','[]'::jsonb),'subtitles_enabled',COALESCE(($2::jsonb->'options'->>'subtitles')::boolean,FALSE)) ELSE spec END
      WHERE id=$1`, id, m)
	return err
}

func (s *Store) SaveRevision(ctx context.Context, clipID string, u domain.EditRequest, manifest json.RawMessage, rerender bool) (domain.Revision, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Revision{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return domain.Revision{}, err
	}
	var currentID int64
	if err = twitchResetClipAllowed(ctx, tx, clipID); err != nil {
		return domain.Revision{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT latest_revision_id FROM clips WHERE clip_id=$1 FOR UPDATE`, clipID).Scan(&currentID); err != nil {
		return domain.Revision{}, mapErr(err)
	}
	prev, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, currentID))
	if err != nil {
		return domain.Revision{}, err
	}
	if prev.Version != u.Version {
		return domain.Revision{}, domain.ErrConflict
	}
	if err = storageRevisionAvailable(ctx, tx, currentID); err != nil {
		return domain.Revision{}, err
	}
	state, bucket, object := prev.RenderState, prev.Bucket, prev.Object
	if rerender {
		state = "pending"
		bucket = ""
		object = ""
	}
	spec, err := json.Marshal(u.EditSpec)
	if err != nil {
		return domain.Revision{}, err
	}
	if len(manifest) == 0 {
		manifest = prev.Manifest
	}
	r, err := scanRevision(tx.QueryRow(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,render_state,bucket,object,manifest)
      VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),$7) RETURNING `+revisionCols, clipID, prev.Version+1, spec, state, bucket, object, manifest))
	if err != nil {
		return domain.Revision{}, err
	}
	if !rerender {
		if _, err = tx.Exec(ctx, `UPDATE clip_revisions r SET media_sha256=old.media_sha256,media_size_bytes=old.media_size_bytes FROM clip_revisions old WHERE r.id=$1 AND old.id=$2`, r.ID, prev.ID); err != nil {
			return domain.Revision{}, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE clips SET latest_revision_id=$2,approval='pending',start_sec=$3,end_sec=$4,title=$5,hashtags=$6 WHERE clip_id=$1`, clipID, r.ID, u.StartSec, u.EndSec, u.Title, u.Hashtags); err != nil {
		return domain.Revision{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_compilation_items(compilation_revision_id,source_revision_id,source_clip_id,ordinal,source_id,media_id) SELECT $1,source_revision_id,source_clip_id,ordinal,source_id,media_id FROM twitch_compilation_items WHERE compilation_revision_id=$2`, r.ID, prev.ID); err != nil {
		return domain.Revision{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL,updated_at=now() WHERE publication_id IN (SELECT id FROM publications WHERE clip_id=$1 AND NOT manual AND started_at IS NULL) AND status='active'`, clipID); err != nil {
		return domain.Revision{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET revision_id=$2,policy_hold_reason=CASE WHEN status IN('scheduled','queued','retry_wait') THEN 'revision_changed' ELSE policy_hold_reason END,status='on_hold',version=version+1,metadata=$3,error=NULL WHERE clip_id=$1 AND NOT manual AND started_at IS NULL AND status NOT IN ('cancelled','published','needs_attention')`, clipID, r.ID, spec); err != nil {
		return domain.Revision{}, err
	}
	if rerender {
		if err = queueRenderAttempt(ctx, tx, r.ID, r.Version, time.Now().UTC()); err != nil {
			return domain.Revision{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Revision{}, err
	}
	return r, nil
}

func (s *Store) ApproveRevision(ctx context.Context, clipID string, now time.Time) error {
	return s.ApproveRevisionWithVersion(ctx, clipID, 0, now)
}
func (s *Store) ApproveRevisionWithVersion(ctx context.Context, clipID string, version int, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if err = approveRevisionTx(ctx, tx, clipID, version, now, nil, ""); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Manual approval and automatic slot admission use the same validation and
// publication creation inside one serialized transaction.
func approveRevisionTx(ctx context.Context, tx pgx.Tx, clipID string, version int, now time.Time, publishAt *time.Time, autoFlow string) error {
	if err := twitchResetClipAllowed(ctx, tx, clipID); err != nil {
		return err
	}
	var err error
	var rid int64
	if err = tx.QueryRow(ctx, `SELECT latest_revision_id FROM clips WHERE clip_id=$1 FOR UPDATE`, clipID).Scan(&rid); err != nil {
		return mapErr(err)
	}
	r, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, rid))
	if err != nil {
		return err
	}
	if version != 0 && r.Version != version {
		return domain.ErrConflict
	}
	if r.RenderState != "ready" || r.Object == "" {
		return fmt.Errorf("%w: rendered revision is not ready", domain.ErrConflict)
	}
	if err = validateGeneratedPublication(r.EditSpec); err != nil {
		return err
	}
	if err = storageRevisionAvailable(ctx, tx, rid); err != nil {
		return err
	}
	if err = requireTwitchSources(ctx, tx, clipID, rid); err != nil {
		return err
	}
	if err = requireFlowFiltersTx(ctx, tx, clipID, rid); err != nil {
		return err
	}
	if publishAt != nil {
		if _, err = tx.Exec(ctx, `UPDATE sub_job_targets t SET start_at=$2 FROM sub_jobs sj,clips c WHERE t.sub_job_id=sj.sub_job_id AND sj.job_id=c.job_id AND c.clip_id=$1`, clipID, *publishAt); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET approved_at=COALESCE(approved_at,$2) WHERE id=$1`, rid, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE clips SET approval='approved',bucket=$2,object=$3 WHERE clip_id=$1`, clipID, r.Bucket, r.Object); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sub_job_targets t SET start_at=$2 FROM sub_jobs sj LEFT JOIN banners b ON b.id=sj.banner_id,clips c WHERE t.sub_job_id=sj.sub_job_id AND c.clip_id=$1 AND sj.job_id=c.job_id AND COALESCE(b.file_name,'')=c.banner_object AND t.start_at IS NULL`, clipID, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO publications(clip_id,sub_job_id,account_id,platform,publish_at,revision_id,timezone,metadata,status)
      SELECT c.clip_id,sj.sub_job_id,a.id,a.platform,COALESCE(t.start_at,$2)+make_interval(secs=>c.idx*t.interval_seconds),$3,t.timezone,$4,'scheduled'
      FROM clips c JOIN sub_jobs sj ON sj.job_id=c.job_id LEFT JOIN banners b ON b.id=sj.banner_id
      JOIN sub_job_targets t ON t.sub_job_id=sj.sub_job_id JOIN accounts a ON a.id=t.account_id
      WHERE c.clip_id=$1 AND COALESCE(b.file_name,'')=c.banner_object
      AND NOT EXISTS(SELECT 1 FROM publications manual JOIN accounts existing ON existing.id=manual.account_id
        WHERE manual.clip_id=c.clip_id AND manual.manual AND existing.platform=a.platform
        AND COALESCE(existing.remote_account_id,'')<>'' AND existing.remote_account_id=a.remote_account_id)
      ON CONFLICT(clip_id,account_id) DO UPDATE SET revision_id=EXCLUDED.revision_id,metadata=EXCLUDED.metadata,status='scheduled',version=publications.version+1,error=NULL,next_attempt_at=now()
      WHERE NOT publications.manual AND publications.started_at IS NULL AND publications.status IN ('on_hold','failed','scheduled')`, clipID, now, rid, mustJSON(r.EditSpec)); err != nil {
		return err
	}
	if err = enforceTwitchCaps(ctx, tx); err != nil {
		return err
	}
	if autoFlow != "" {
		_, err = tx.Exec(ctx, `UPDATE publications SET auto_flow_id=$2,auto_hold=FALSE WHERE clip_id=$1 AND NOT manual AND started_at IS NULL`, clipID, autoFlow)
	} else {
		_, err = tx.Exec(ctx, `UPDATE publications SET auto_flow_id=NULL,auto_hold=FALSE WHERE clip_id=$1 AND NOT manual AND started_at IS NULL`, clipID)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE twitch_outputs SET auto_eligible=FALSE WHERE clip_id=$1`, clipID)
		}
	}
	if err != nil {
		return err
	}
	rows, e := tx.Query(ctx, `SELECT id FROM publications WHERE clip_id=$1 AND NOT manual AND started_at IS NULL`, clipID)
	if e != nil {
		return e
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	rows.Close()
	if e = rows.Err(); e != nil {
		return e
	}
	for _, id := range ids {
		if e = setPublicationOverrideTx(ctx, tx, id, false, now); e != nil {
			return e
		}
		if e = validatePublicationPolicyTx(ctx, tx, id, now); e != nil {
			return e
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_preparation_reservations SET state='consumed' WHERE clip_id=$1 AND state='active'`, clipID); err != nil {
		return err
	}
	return err
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (s *Store) RejectRevision(ctx context.Context, clipID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_imports SET rejected=TRUE,updated_at=now() WHERE clip_id=$1`, clipID); err != nil {
		return err
	}
	if err = rejectTwitchDependencies(ctx, tx, clipID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE clips SET approval='rejected' WHERE clip_id=$1`, clipID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET status=CASE WHEN started_at IS NULL THEN 'cancelled' ELSE 'cancel_pending' END,cancel_requested=TRUE,version=version+1,next_attempt_at=now() WHERE clip_id=$1 AND status NOT IN ('published','cancelled')`, clipID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FinishRevision(ctx context.Context, id int64, bucket, object, errMsg string) error {
	state := "ready"
	if errMsg != "" {
		state = "failed"
	}
	_, err := s.pool.Exec(ctx, `UPDATE clip_revisions SET render_state=$2,bucket=NULLIF($3,''),object=NULLIF($4,''),error=NULLIF($5,'') WHERE id=$1`, id, state, bucket, object, errMsg)
	return err
}

func (s *Store) Targets(ctx context.Context, id string) ([]domain.AccountTarget, error) {
	rows, err := s.pool.Query(ctx, `SELECT account_id,start_at,interval_seconds,timezone,legacy_shared FROM sub_job_targets WHERE sub_job_id=$1 ORDER BY account_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AccountTarget{}
	for rows.Next() {
		var t domain.AccountTarget
		if err = rows.Scan(&t.AccountID, &t.StartAt, &t.IntervalSeconds, &t.Timezone, &t.LegacyShared); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) TargetVersion(ctx context.Context, id string) (int, error) {
	var version int
	err := s.pool.QueryRow(ctx, `SELECT workflow_version FROM sub_jobs WHERE sub_job_id=$1`, id).Scan(&version)
	return version, mapErr(err)
}
func (s *Store) ReplaceTargets(ctx context.Context, id string, version int, targets []domain.AccountTarget) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	var current int
	if err = tx.QueryRow(ctx, `SELECT workflow_version FROM sub_jobs WHERE sub_job_id=$1 FOR UPDATE`, id).Scan(&current); err != nil {
		return mapErr(err)
	}
	if version != current {
		return domain.ErrConflict
	}
	var kind string
	if err = tx.QueryRow(ctx, `SELECT j.source_kind FROM sub_jobs s JOIN jobs j ON j.job_id=s.job_id WHERE s.sub_job_id=$1`, id).Scan(&kind); err != nil {
		return mapErr(err)
	}
	if kind == "twitch" || kind == "twitch_compilation" {
		var pinned int64
		if e := tx.QueryRow(ctx, `SELECT o.account_id FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id JOIN sub_jobs sj ON sj.job_id=c.job_id WHERE sj.sub_job_id=$1 LIMIT 1`, id).Scan(&pinned); e == nil {
			if len(targets) != 1 || targets[0].AccountID != pinned {
				return fmt.Errorf("%w: назначение ролика закреплено за каналом потока", domain.ErrInvalid)
			}
		} else if e != pgx.ErrNoRows {
			return e
		} else {
			settings, e := readTwitchSettings(ctx, tx)
			if e != nil {
				return e
			}
			if len(targets) != 1 || targets[0].AccountID != settings.TargetAccountID || settings.TargetAccountID <= 0 {
				return fmt.Errorf("%w: Twitch drafts use the single configured YouTube account", domain.ErrInvalid)
			}
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM sub_job_targets WHERE sub_job_id=$1`, id); err != nil {
		return err
	}
	ids := []int64{}
	for _, t := range targets {
		ids = append(ids, t.AccountID)
		if _, err = tx.Exec(ctx, `INSERT INTO sub_job_targets(sub_job_id,account_id,start_at,interval_seconds,timezone)VALUES($1,$2,$3,$4,$5)`, id, t.AccountID, t.StartAt, t.IntervalSeconds, t.Timezone); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE sub_job_targets t SET start_at=(SELECT min(r.approved_at) FROM sub_jobs sj JOIN clips c ON c.job_id=sj.job_id LEFT JOIN banners b ON b.id=sj.banner_id JOIN clip_revisions r ON r.id=c.latest_revision_id WHERE sj.sub_job_id=t.sub_job_id AND c.banner_object=COALESCE(b.file_name,'') AND r.approved_at IS NOT NULL) WHERE sub_job_id=$1 AND start_at IS NULL`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE sub_jobs SET account_ids=$2,workflow_version=workflow_version+1 WHERE sub_job_id=$1`, id, ids); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications p SET publish_at=COALESCE(t.start_at,p.publish_at)+make_interval(secs=>c.idx*t.interval_seconds),status=CASE WHEN p.status IN ('queued','retry_wait') THEN 'scheduled' ELSE p.status END,version=p.version+1,error=NULL FROM sub_job_targets t,clips c WHERE p.sub_job_id=$1 AND t.sub_job_id=p.sub_job_id AND t.account_id=p.account_id AND c.clip_id=p.clip_id AND p.started_at IS NULL AND p.status NOT IN ('published','cancelled','needs_attention')`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications p SET status='cancelled',cancel_requested=TRUE,version=version+1 WHERE sub_job_id=$1 AND started_at IS NULL AND NOT(account_id=ANY($2)) AND status NOT IN ('published','cancelled')`, id, ids); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE status='active' AND publication_id IN(SELECT id FROM publications WHERE sub_job_id=$1 AND started_at IS NULL)`, id); err != nil {
		return err
	}
	// New recipients also receive already-approved clips. Started and legacy work
	// is preserved by the unique clip/account constraint and the conflict clause.
	if _, err = tx.Exec(ctx, `INSERT INTO publications(clip_id,sub_job_id,account_id,platform,publish_at,revision_id,timezone,metadata,status)
      SELECT c.clip_id,sj.sub_job_id,a.id,a.platform,COALESCE(t.start_at,r.approved_at)+make_interval(secs=>c.idx*t.interval_seconds),r.id,t.timezone,r.spec,'scheduled'
      FROM sub_jobs sj JOIN clips c ON c.job_id=sj.job_id LEFT JOIN banners b ON b.id=sj.banner_id
      JOIN clip_revisions r ON r.id=c.latest_revision_id JOIN sub_job_targets t ON t.sub_job_id=sj.sub_job_id JOIN accounts a ON a.id=t.account_id
      WHERE sj.sub_job_id=$1 AND c.banner_object=COALESCE(b.file_name,'') AND r.approved_at IS NOT NULL AND r.render_state='ready'
      ON CONFLICT(clip_id,account_id) DO NOTHING`, id); err != nil {
		return err
	}
	if err = enforceTwitchCaps(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RevisionHistory(ctx context.Context, clipID string) ([]domain.Revision, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE clip_id=$1 AND NOT publication_only ORDER BY revision_no DESC LIMIT 100`, clipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Revision{}
	for rows.Next() {
		r, e := scanRevision(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) RetryRevision(ctx context.Context, clipID string, version int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if err = twitchResetClipAllowed(ctx, tx, clipID); err != nil {
		return err
	}
	var rid int64
	if err = tx.QueryRow(ctx, `SELECT latest_revision_id FROM clips WHERE clip_id=$1 FOR UPDATE`, clipID).Scan(&rid); err != nil {
		return mapErr(err)
	}
	r, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1 FOR UPDATE`, rid))
	if err != nil {
		return err
	}
	if r.Version != version {
		return domain.ErrConflict
	}
	if err = storageRevisionAvailable(ctx, tx, rid); err != nil {
		return err
	}
	if r.RenderState == "ready" || r.RenderState == "pending" || r.RenderState == "rendering" {
		return nil
	}
	var source map[string]any
	if json.Unmarshal(r.Manifest, &source) != nil || source["original"] == nil {
		return fmt.Errorf("%w: source unavailable", domain.ErrConflict)
	}

	if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET render_state='pending',error=NULL,render_wait_reason='',render_deadline_reason='worker_wait_timeout',render_first_queued_at=NULL,render_dispatch_deadline=NULL,render_next_attempt_at=now() WHERE id=$1`, rid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE jobs j SET status='processing',fail_place='',updated_at=now() FROM clips c WHERE c.job_id=j.job_id AND c.latest_revision_id=$1 AND j.source_kind IN('twitch','twitch_compilation')`, rid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_imports i SET state='rendering',error=NULL,updated_at=now() FROM clips c WHERE c.clip_id=i.clip_id AND c.latest_revision_id=$1`, rid); err != nil {
		return err
	}
	if err = queueRenderAttempt(ctx, tx, rid, r.Version, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) RestoreRevision(ctx context.Context, clipID string, version int, targetID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if err = twitchResetClipAllowed(ctx, tx, clipID); err != nil {
		return err
	}
	var rid int64
	if err = tx.QueryRow(ctx, `SELECT latest_revision_id FROM clips WHERE clip_id=$1 FOR UPDATE`, clipID).Scan(&rid); err != nil {
		return mapErr(err)
	}
	current, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, rid))
	if err != nil {
		return err
	}
	if current.Version != version {
		return domain.ErrConflict
	}
	old, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1 AND clip_id=$2 AND NOT publication_only`, targetID, clipID))
	if err != nil {
		return err
	}
	if old.RenderState != "ready" || old.Object == "" {
		return fmt.Errorf("%w: selected revision has no finished asset", domain.ErrConflict)
	}
	if err = storageRevisionAvailable(ctx, tx, targetID); err != nil {
		return err
	}
	spec := mustJSON(old.EditSpec)
	var nextID int64
	if err = tx.QueryRow(ctx, `INSERT INTO clip_revisions(clip_id,revision_no,spec,render_state,bucket,object,manifest)VALUES($1,$2,$3,'ready',$4,$5,$6)RETURNING id`, clipID, version+1, spec, old.Bucket, old.Object, old.Manifest).Scan(&nextID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE clip_revisions r SET media_sha256=old.media_sha256,media_size_bytes=old.media_size_bytes FROM clip_revisions old WHERE r.id=$1 AND old.id=$2`, nextID, targetID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE clips SET latest_revision_id=$2,approval='pending',start_sec=$3,end_sec=$4,title=$5,hashtags=$6 WHERE clip_id=$1`, clipID, nextID, old.StartSec, old.EndSec, old.Title, old.Hashtags); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO twitch_compilation_items(compilation_revision_id,source_revision_id,source_clip_id,ordinal,source_id,media_id) SELECT $1,source_revision_id,source_clip_id,ordinal,source_id,media_id FROM twitch_compilation_items WHERE compilation_revision_id=$2`, nextID, targetID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE status='active' AND publication_id IN(SELECT id FROM publications WHERE clip_id=$1 AND NOT manual AND started_at IS NULL)`, clipID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET revision_id=$2,metadata=$3,policy_hold_reason=CASE WHEN status IN('scheduled','queued','retry_wait')THEN 'revision_changed' ELSE policy_hold_reason END,status='on_hold',version=version+1,error=NULL WHERE clip_id=$1 AND NOT manual AND started_at IS NULL AND status NOT IN ('published','cancelled','needs_attention')`, clipID, nextID, spec); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
