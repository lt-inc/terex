package postgres

import (
	"context"
	"fmt"
	"terex/main-be/internal/domain"
	"time"
)

func (s *Store) SaveTikTokConnection(ctx context.Context, c domain.TikTokConnection) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var platform, remote string
	var version int64
	if err = tx.QueryRow(ctx, `SELECT platform,COALESCE(remote_account_id,''),session_version FROM accounts WHERE id=$1 FOR UPDATE`, c.AccountID).Scan(&platform, &remote, &version); err != nil {
		return mapErr(err)
	}
	if platform != "tiktok" || (remote != "" && c.RemoteAccountID != "" && remote != c.RemoteAccountID) || c.SessionVersion < version {
		return fmt.Errorf("%w: TikTok account identity or session changed", domain.ErrConflict)
	}
	state := c.Status
	switch state {
	case "connected", "needs_reauth", "disconnected", "challenge", "pairing":
	default:
		return domain.ErrInvalid
	}
	if state == "connected" && (c.RemoteAccountID == "" || c.SessionRef == "" || c.SessionVersion <= 0) {
		return domain.ErrInvalid
	}
	if c.RemoteAccountID == "" {
		c.RemoteAccountID = remote
	}
	ref := map[string]any{"bucket": "tiktok-sessions", "object": c.SessionRef, "display_name": c.DisplayName, "session_version": c.SessionVersion}
	if _, err = tx.Exec(ctx, `UPDATE accounts SET remote_account_id=NULLIF($2,''),connection_state=$3,session_version=GREATEST(session_version,$4),credential_ref=CASE WHEN $5::jsonb->>'object'<>'' THEN $5::jsonb ELSE credential_ref END,connection_checked_at=now(),connection_error=NULLIF($6,''),connection_version=connection_version+1 WHERE id=$1`, c.AccountID, c.RemoteAccountID, state, c.SessionVersion, mustJSON(ref), c.Error); err != nil {
		return err
	}
	if state == "connected" {
		_, err = tx.Exec(ctx, `UPDATE publications SET status=CASE WHEN started_at IS NULL THEN 'scheduled' ELSE 'retry_wait' END,error=NULL,error_code='',next_attempt_at=$2 WHERE account_id=$1 AND platform='tiktok' AND status='blocked' AND error_code IN('auth_required','operator_required') AND NOT EXISTS(SELECT 1 FROM publication_attempts a WHERE a.publication_id=publications.id AND COALESCE((a.checkpoint->>'final_request_started')::bool,FALSE))`, c.AccountID, time.Now().UTC())
	}
	return tx.Commit(ctx)
}

func (s *Store) TikTokAttemptAllowed(ctx context.Context, attempt string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publication_attempts a JOIN publications p ON p.id=a.publication_id WHERE a.id=$1 AND p.platform='tiktok')`, attempt).Scan(&ok)
	return ok, err
}
func (s *Store) TikTokPublicationAllowed(ctx context.Context, pid int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publications WHERE id=$1 AND platform='tiktok')`, pid).Scan(&ok)
	return ok, err
}

func (s *Store) ReleaseTikTokOperation(ctx context.Context, attempt, token string, now time.Time) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE tiktok_worker_operations SET state='released',released_at=COALESCE(released_at,$3) WHERE attempt_id=$1 AND lease_token=$2`, attempt, token, now)
	if err != nil || tag.RowsAffected() == 1 {
		return tag.RowsAffected() == 1, err
	}
	// Nothing was started when the worker died before its first checkpoint.
	// A mismatched token for an existing operation is still rejected.
	var exists bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tiktok_worker_operations WHERE attempt_id=$1)`, attempt).Scan(&exists)
	return !exists, err
}

func (s *Store) ReconcileTikTok(ctx context.Context, pid int64, version int, remoteID, remoteURL string, operatorConfirmed bool, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE publications SET upload_id=$3,external_url=$4,status='published',published_at=COALESCE(published_at,$5),last_verified_at=$5,error=NULL WHERE id=$1 AND version=$2 AND platform='tiktok' AND status IN('needs_attention','verifying','processing') AND NOT EXISTS(SELECT 1 FROM publication_attempts a WHERE a.publication_id=publications.id AND a.status='active' AND a.worker_id<>'')`, pid, version, remoteID, remoteURL, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	if err = insertEvent(ctx, tx, pid, "", "reconciled", "published", map[string]any{"remote_id": remoteID, "remote_url": remoteURL, "operator_confirmed": operatorConfirmed}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RetryTikTokAfterAbsence(ctx context.Context, pid int64, version int, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	var clip string
	if err = tx.QueryRow(ctx, `SELECT clip_id FROM publications WHERE id=$1 AND version=$2 AND platform='tiktok' AND status='needs_attention' FOR UPDATE`, pid, version).Scan(&clip); err != nil {
		return mapErr(err)
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM publication_attempts WHERE publication_id=$1 AND status='active' AND worker_id<>'' AND lease_until>$2) OR EXISTS(SELECT 1 FROM tiktok_worker_operations WHERE publication_id=$1 AND state='active')`, pid, now).Scan(&active); err != nil {
		return err
	}
	if active {
		return fmt.Errorf("%w: TikTok worker must finish before an explicit retry", domain.ErrConflict)
	}
	if err = twitchResetClipAllowed(ctx, tx, clip); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE publication_id=$1 AND status='active'`, pid); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET version=version+1,status='scheduled',started_at=NULL,upload_id=NULL,external_url=NULL,uploaded_at=NULL,processed_at=NULL,last_verified_at=NULL,publish_at=$2,next_attempt_at=$2,error=NULL,error_code='',auto_flow_id=NULL,auto_hold=FALSE,cancel_requested=FALSE,metadata=metadata||'{"retry_authorized":true}'::jsonb WHERE id=$1`, pid, now); err != nil {
		return err
	}
	if err = insertEvent(ctx, tx, pid, "", "operator_confirmed_absent", "scheduled", map[string]any{"previous_version": version, "retry_authorized_version": version + 1}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
