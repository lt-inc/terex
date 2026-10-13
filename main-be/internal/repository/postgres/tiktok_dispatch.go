package postgres

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

func reconcileTikTokDispatchTx(ctx context.Context, tx pgx.Tx, now time.Time) error {
	// A lost reply after the non-idempotent submit never creates another upload.
	if _, err := tx.Exec(ctx, `UPDATE publications p SET status='needs_attention',error_code='upload_unknown',error='Результат TikTok-публикации неизвестен; проверьте аккаунт',next_attempt_at=$1 FROM publication_attempts a WHERE a.publication_id=p.id AND p.platform='tiktok' AND a.status='active' AND a.execution_started_at IS NOT NULL AND a.lease_until<$1 AND COALESCE((a.checkpoint->>'final_request_started')::bool,FALSE)`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE publications p SET status='on_hold',auto_hold=TRUE,policy_hold_reason='worker_wait_timeout',error_code='worker_wait_timeout',error='TikTok worker не принял задание до крайнего срока' FROM publication_attempts a WHERE a.publication_id=p.id AND p.platform='tiktok' AND a.status='active' AND a.execution_started_at IS NULL AND a.dispatch_deadline<=$1`, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE publication_attempts a SET status='expired',lease_token=NULL WHERE a.status='active' AND a.execution_started_at IS NULL AND a.dispatch_deadline<=$1 AND EXISTS(SELECT 1 FROM publications p WHERE p.id=a.publication_id AND p.platform='tiktok')`, now); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT a.id,a.publication_id,a.publication_version FROM publication_attempts a JOIN publications p ON p.id=a.publication_id WHERE a.status='active' AND a.execution_started_at IS NULL AND a.dispatch_deadline>$1 AND a.last_dispatched_at<=$1::timestamptz-interval '15 minutes' AND p.platform='tiktok' AND p.status='queued' FOR UPDATE OF a`, now)
	if err != nil {
		return err
	}
	type delivery struct {
		id      string
		pid     int64
		version int
	}
	var commands []delivery
	for rows.Next() {
		var c delivery
		if err = rows.Scan(&c.id, &c.pid, &c.version); err != nil {
			rows.Close()
			return err
		}
		commands = append(commands, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, c := range commands {
		payload := map[string]any{"kind": "publish", "publication_id": c.pid, "attempt_id": c.id, "expected_version": c.version}
		if _, err = tx.Exec(ctx, `INSERT INTO publication_outbox(id,publication_id,kind,payload)VALUES($1,$2,'publish_tiktok',$3)`, uuid.NewString(), c.pid, mustJSON(payload)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET last_dispatched_at=$2 WHERE id=$1`, c.id, now); err != nil {
			return err
		}
	}
	// Catch-up of browser uploads requires a fresh operator decision after 30m.
	_, err = tx.Exec(ctx, `UPDATE publications SET status='on_hold',auto_hold=TRUE,policy_hold_reason='missed_tiktok_slot',error_code='missed_tiktok_slot',error='TikTok: назначенное время пропущено более чем на 30 минут' WHERE platform='tiktok' AND status='scheduled' AND started_at IS NULL AND publish_at<$1::timestamptz-interval '30 minutes' AND EXISTS(SELECT 1 FROM accounts a WHERE a.id=publications.account_id AND a.session_version>0 AND a.credential_ref->>'bucket'='tiktok-sessions')`, now)
	return err
}
