package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"terex/proto/workflow"
)

// A revision's initial deadline survives transport redelivery and execution
// retries. Only an explicit retry starts a new bounded preparation window.
func renderDispatchDeadline(ctx context.Context, tx pgx.Tx, rid int64, now time.Time) (time.Time, int64, string, error) {
	var first, saved *time.Time
	reason := "worker_wait_timeout"
	if err := tx.QueryRow(ctx, `SELECT render_first_queued_at,render_dispatch_deadline,render_deadline_reason FROM clip_revisions WHERE id=$1`, rid).Scan(&first, &saved, &reason); err != nil {
		return time.Time{}, 0, reason, err
	}
	if first == nil {
		first = &now
	}
	deadline := first.Add(24 * time.Hour)
	if saved != nil && saved.Before(deadline) {
		deadline = *saved
	}
	var sourceDeadline, prepDeadline *time.Time
	var epoch int64
	err := tx.QueryRow(ctx, `SELECT
 CASE WHEN j.source_kind IN('twitch','twitch_compilation') THEN ctl.epoch ELSE 0 END,
 (SELECT min((i.request->'source'->>'created_at')::timestamptz+make_interval(hours=>COALESCE(NULLIF((f.config->>'max_source_age_hours')::int,0),72)))
 FROM twitch_outputs o JOIN twitch_flows f ON f.id=o.flow_id
 JOIN LATERAL(SELECT o.source_id id WHERE o.source_id IS NOT NULL UNION SELECT source_id FROM twitch_compilation_items WHERE compilation_revision_id=$1 AND source_id IS NOT NULL) refs ON true
 JOIN twitch_imports i ON i.id=refs.id WHERE o.clip_id=r.clip_id),
 (SELECT min(pr.slot_at) FROM twitch_preparation_reservations pr WHERE pr.clip_id=r.clip_id AND pr.state='active')
 FROM clip_revisions r JOIN clips c ON c.clip_id=r.clip_id JOIN jobs j ON j.job_id=c.job_id CROSS JOIN twitch_reset_control ctl WHERE r.id=$1 AND ctl.id=1`, rid).Scan(&epoch, &sourceDeadline, &prepDeadline)
	if err != nil {
		return time.Time{}, 0, reason, err
	}
	if sourceDeadline != nil && !sourceDeadline.After(deadline) {
		deadline = *sourceDeadline
		reason = "source_expired"
	}
	if prepDeadline != nil && prepDeadline.Before(deadline) {
		deadline = *prepDeadline
		reason = "worker_wait_timeout"
	}
	_, err = tx.Exec(ctx, `UPDATE clip_revisions SET render_first_queued_at=COALESCE(render_first_queued_at,$2),render_dispatch_deadline=$3,render_deadline_reason=$4 WHERE id=$1`, rid, *first, deadline, reason)
	return deadline, epoch, reason, err
}

func holdRenderDispatch(ctx context.Context, tx pgx.Tx, rid int64, reason string) error {
	_, err := tx.Exec(ctx, `UPDATE clip_revisions SET render_state='waiting_worker',render_wait_reason=$2,error=CASE WHEN $2='source_expired' THEN 'Исходники устарели до начала рендера; требуется проверка перед повтором' ELSE 'Worker не начал рендер в допустимый срок; запустите повтор после проверки' END WHERE id=$1 AND render_state IN('pending','rendering','queued')`, rid, reason)
	return err
}

func queueRenderAttempt(ctx context.Context, tx pgx.Tx, rid int64, version int, now time.Time) error {
	deadline, epoch, reason, err := renderDispatchDeadline(ctx, tx, rid, now)
	if err != nil {
		return err
	}
	if !deadline.After(now) {
		return holdRenderDispatch(ctx, tx, rid, reason)
	}
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO render_attempts(id,revision_id,lease_until,created_at,dispatch_deadline,dispatch_retry_at,epoch,twitch)VALUES($1,$2,$3,$4::timestamptz,$3,$4::timestamptz+interval '15 minutes',$5,(SELECT j.source_kind IN('twitch','twitch_compilation') FROM clip_revisions r JOIN clips c ON c.clip_id=r.clip_id JOIN jobs j ON j.job_id=c.job_id WHERE r.id=$2))`, id, rid, deadline, now, epoch); err != nil {
		return err
	}
	return queueRenderCommand(ctx, tx, id, rid, version)
}
func queueRenderCommand(ctx context.Context, tx pgx.Tx, id string, rid int64, version int) error {
	_, err := tx.Exec(ctx, `INSERT INTO publication_outbox(id,kind,payload)SELECT $1,'render_revision',$2 WHERE NOT EXISTS(SELECT 1 FROM publication_outbox WHERE kind='render_revision' AND delivered_at IS NULL AND payload->>'attempt_id'=$3)`, uuid.NewString(), mustJSON(workflow.Command{Kind: workflow.KindRender, RevisionID: rid, AttemptID: id, ExpectedVersion: version}), id)
	return err
}

// Draft creation precedes attaching its flow/source rows. Tighten the queued
// envelope in that same transaction after those immutable references exist.
func tightenQueuedRender(ctx context.Context, tx pgx.Tx, rid int64, now time.Time) error {
	deadline, _, reason, err := renderDispatchDeadline(ctx, tx, rid, now)
	if err != nil {
		return err
	}
	if !deadline.After(now) {
		if _, err = tx.Exec(ctx, `UPDATE render_attempts SET status='expired',lease_token=NULL WHERE revision_id=$1 AND status='active' AND execution_started_at IS NULL`, rid); err != nil {
			return err
		}
		return holdRenderDispatch(ctx, tx, rid, reason)
	}
	_, err = tx.Exec(ctx, `UPDATE render_attempts SET dispatch_deadline=$2,lease_until=$2 WHERE revision_id=$1 AND status='active' AND execution_started_at IS NULL AND dispatch_deadline IS DISTINCT FROM $2`, rid, deadline)
	return err
}

func reconcileRenderDispatch(ctx context.Context, tx pgx.Tx, now time.Time, limit int) error {
	if _, err := tx.Exec(ctx, `UPDATE render_attempts SET status='expired',lease_token=NULL WHERE status='active' AND execution_started_at IS NOT NULL AND lease_until<=$1`, now); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT a.id,a.revision_id,r.revision_no FROM render_attempts a JOIN clip_revisions r ON r.id=a.revision_id WHERE a.status='active' AND a.execution_started_at IS NULL AND(a.dispatch_retry_at<=$1 OR a.dispatch_deadline<=$1) ORDER BY a.created_at LIMIT $2 FOR UPDATE OF a SKIP LOCKED`, now, limit)
	if err != nil {
		return err
	}
	type pending struct {
		id      string
		rid     int64
		version int
	}
	items := []pending{}
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.rid, &p.version); err != nil {
			break
		}
		items = append(items, p)
	}
	rows.Close()
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return err
	}
	for _, p := range items {
		deadline, _, reason, e := renderDispatchDeadline(ctx, tx, p.rid, now)
		if e != nil {
			return e
		}
		if !deadline.After(now) {
			if _, e = tx.Exec(ctx, `UPDATE render_attempts SET status='expired',lease_token=NULL WHERE id=$1`, p.id); e != nil {
				return e
			}
			if e = holdRenderDispatch(ctx, tx, p.rid, reason); e != nil {
				return e
			}
			continue
		}
		if e = queueRenderCommand(ctx, tx, p.id, p.rid, p.version); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE render_attempts SET dispatch_deadline=$2,lease_until=$2,dispatch_retry_at=$3::timestamptz+interval '15 minutes' WHERE id=$1`, p.id, deadline, now); e != nil {
			return e
		}
	}
	return nil
}
