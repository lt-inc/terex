package postgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"time"
)

// Approval marks the exact ready version. Scheduling remains a separate flow
// decision, and operator holds/cancellations are never implicitly resumed.
func autoApproveTwitchReadyTx(ctx context.Context, tx pgx.Tx, clip string, now time.Time) error {
	if err := twitchResetAllowed(ctx, tx); err != nil {
		return err
	}
	var rid int64
	var bucket, object, state, approval string
	err := tx.QueryRow(ctx, `SELECT r.id,COALESCE(r.bucket,''),COALESCE(r.object,''),r.render_state,c.approval FROM clips c JOIN jobs j ON j.job_id=c.job_id JOIN clip_revisions r ON r.id=c.latest_revision_id WHERE c.clip_id=$1 AND j.source_kind IN('twitch','twitch_compilation')`, clip).Scan(&rid, &bucket, &object, &state, &approval)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "ready" || object == "" || approval == "rejected" {
		return nil
	}
	for _, check := range []func() error{func() error { return storageRevisionAvailable(ctx, tx, rid) }, func() error { return requireTwitchSources(ctx, tx, clip, rid) }, func() error { return requireFlowFiltersTx(ctx, tx, clip, rid) }} {
		if e := check(); e != nil {
			if errors.Is(e, domain.ErrConflict) {
				return nil
			}
			return e
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET approved_at=COALESCE(approved_at,$2)WHERE id=$1`, rid, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE clips SET approval='approved',bucket=$2,object=$3 WHERE clip_id=$1`, clip, bucket, object); err != nil {
		return err
	}
	// Manual flows still need a calendar object after removing the approve
	// button. It is held until the operator chooses a date or publishes now.
	if _, err = tx.Exec(ctx, `INSERT INTO publications(clip_id,sub_job_id,account_id,platform,publish_at,revision_id,timezone,metadata,status,policy_hold_reason,error)
 SELECT c.clip_id,sj.sub_job_id,a.id,a.platform,COALESCE(t.start_at,$2),$3,t.timezone,r.spec,'on_hold','manual_schedule','Выберите время публикации в календаре'
 FROM clips c JOIN twitch_outputs o ON o.clip_id=c.clip_id JOIN twitch_flows f ON f.id=o.flow_id JOIN sub_jobs sj ON sj.job_id=c.job_id JOIN sub_job_targets t ON t.sub_job_id=sj.sub_job_id JOIN accounts a ON a.id=t.account_id JOIN clip_revisions r ON r.id=$3
 WHERE c.clip_id=$1 AND f.enabled AND NOT f.archived AND NOT f.auto_publish ON CONFLICT(clip_id,account_id)DO NOTHING`, clip, now, rid); err != nil {
		return err
	}
	// Only an edit-created hold can be resumed. Existing provider sessions keep
	// their original revision because started_at must still be NULL.
	if _, err = tx.Exec(ctx, `UPDATE publications p SET status='scheduled',auto_hold=FALSE,policy_hold_reason='',revision_id=$2,metadata=r.spec,error=NULL,next_attempt_at=$3,version=p.version+1 FROM clip_revisions r WHERE r.id=$2 AND p.clip_id=$1 AND p.started_at IS NULL AND p.status='on_hold' AND p.policy_hold_reason='revision_changed' AND NOT p.cancel_requested AND NOT EXISTS(SELECT 1 FROM twitch_outputs o JOIN twitch_flows f ON f.id=o.flow_id WHERE o.clip_id=$1 AND(f.archived OR NOT f.enabled OR(p.auto_flow_id IS NOT NULL AND NOT f.auto_publish)))`, clip, rid, now); err != nil {
		return err
	}
	return nil
}

func (s *Store) autoApproveTwitchReady(ctx context.Context, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT c.clip_id FROM clips c JOIN jobs j ON j.job_id=c.job_id JOIN clip_revisions r ON r.id=c.latest_revision_id WHERE j.source_kind IN('twitch','twitch_compilation') AND r.render_state='ready' AND c.approval<>'rejected' AND(r.approved_at IS NULL OR EXISTS(SELECT 1 FROM publications p WHERE p.clip_id=c.clip_id AND p.status='on_hold' AND p.policy_hold_reason='revision_changed') OR EXISTS(SELECT 1 FROM twitch_outputs o JOIN twitch_flows f ON f.id=o.flow_id WHERE o.clip_id=c.clip_id AND f.enabled AND NOT f.archived AND NOT f.auto_publish AND NOT EXISTS(SELECT 1 FROM publications p WHERE p.clip_id=c.clip_id AND p.account_id=o.account_id)))`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err = autoApproveTwitchReadyTx(ctx, tx, id, now); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
