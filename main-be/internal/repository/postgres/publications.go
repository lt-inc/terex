package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
)

const publicationCols = `p.id,p.clip_id,COALESCE(p.revision_id,0),p.account_id,a.name,p.platform,p.publish_at,p.timezone,p.status,p.version,p.metadata,p.cancel_requested,p.upload_id,p.external_url,p.error,p.uploaded_at,p.processed_at,p.published_at,p.last_verified_at,p.started_at`

func scanPublication(row pgx.Row) (domain.PublicationRecord, error) {
	var p domain.PublicationRecord
	var meta []byte
	err := row.Scan(&p.ID, &p.ClipID, &p.RevisionID, &p.AccountID, &p.AccountName, &p.Platform, &p.PublishAt, &p.Timezone, &p.Status, &p.Version, &meta, &p.CancelRequested, &p.UploadID, &p.ExternalURL, &p.Error, &p.UploadedAt, &p.ProcessedAt, &p.PublishedAt, &p.LastVerifiedAt, &p.StartedAt)
	if err == nil {
		err = json.Unmarshal(meta, &p.PublicationMetadata)
	}
	p.State = p.Status
	return p, mapErr(err)
}
func (s *Store) GetPublicationRecord(ctx context.Context, id int64) (domain.PublicationRecord, error) {
	return scanPublication(s.pool.QueryRow(ctx, `SELECT `+publicationCols+` FROM publications p JOIN accounts a ON a.id=p.account_id WHERE p.id=$1`, id))
}
func (s *Store) ListPublicationRecords(ctx context.Context, f domain.PublicationFilter) ([]domain.PublicationRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+publicationCols+` FROM publications p JOIN accounts a ON a.id=p.account_id
      WHERE ($1::timestamptz IS NULL OR p.publish_at>=$1) AND ($2::timestamptz IS NULL OR p.publish_at<$2)
      AND ($3::bigint=0 OR p.account_id=$3) AND ($4='' OR p.platform=$4) AND ($5='' OR p.status=$5)
      ORDER BY p.publish_at,p.id LIMIT $6 OFFSET $7`, f.From, f.To, f.AccountID, f.Platform, f.State, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PublicationRecord{}
	for rows.Next() {
		p, e := scanPublication(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) PatchPublication(ctx context.Context, id int64, u domain.PublicationPatch) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	p, err := scanPublication(tx.QueryRow(ctx, `SELECT `+publicationCols+` FROM publications p JOIN accounts a ON a.id=p.account_id WHERE p.id=$1 FOR UPDATE OF p`, id))
	if err != nil {
		return err
	}
	if p.Version != u.Version {
		return domain.ErrConflict
	}
	if p.StartedAt != nil || p.Status == "published" || p.Status == "cancelled" {
		return fmt.Errorf("%w: publication has started; its revision and metadata are frozen", domain.ErrConflict)
	}
	if u.Title != nil {
		p.Title = *u.Title
	}
	if u.Description != nil {
		p.Description = *u.Description
	}
	if u.Hashtags != nil {
		p.Hashtags = *u.Hashtags
	}
	if u.PublishAt != nil {
		p.PublishAt = *u.PublishAt
	}
	if u.Timezone != nil {
		p.Timezone = *u.Timezone
	}
	state := "on_hold"
	if u.Title == nil && u.Description == nil && u.Hashtags == nil {
		state = p.Status
		if state == "queued" || state == "retry_wait" {
			state = "scheduled"
		}
	}
	_, err = tx.Exec(ctx, `UPDATE publications SET publish_at=$2,timezone=$3,metadata=$4,status=$5,version=version+1,error=NULL WHERE id=$1`, id, p.PublishAt, p.Timezone, mustJSON(p.PublicationMetadata), state)
	if err != nil {
		return err
	}
	if err = manualPublication(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE publication_id=$1 AND status='active'`, id); err != nil {
		return err
	}
	if err = insertEvent(ctx, tx, id, "", "edited", state, map[string]any{"requires_reapproval": state == "on_hold"}); err != nil {
		return err
	}
	if err = enforceTwitchCaps(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ActPublication(ctx context.Context, id int64, action string, version int, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	p, err := scanPublication(tx.QueryRow(ctx, `SELECT `+publicationCols+` FROM publications p JOIN accounts a ON a.id=p.account_id WHERE p.id=$1 FOR UPDATE OF p`, id))
	if err != nil {
		return err
	}
	if p.Version != version {
		return domain.ErrConflict
	}
	state := p.Status
	cancel := p.CancelRequested
	at := p.PublishAt
	switch action {
	case "approve":
		if err = requireTwitchSources(ctx, tx, p.ClipID, p.RevisionID); err != nil {
			return err
		}
		if p.Status != "on_hold" && p.Status != "scheduled" && p.Status != "needs_attention" {
			return domain.ErrConflict
		}
		var approved bool
		var ready bool
		if err = tx.QueryRow(ctx, `SELECT approved_at IS NOT NULL,render_state='ready' AND object IS NOT NULL FROM clip_revisions WHERE id=$1`, p.RevisionID).Scan(&approved, &ready); err != nil {
			return mapErr(err)
		}
		if !ready {
			return fmt.Errorf("%w: rendered revision is not ready", domain.ErrConflict)
		}
		if !approved {
			if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET approved_at=$2 WHERE id=$1`, p.RevisionID, now); err != nil {
				return err
			}
		}
		state = "scheduled"
		cancel = false
	case "retry":
		if p.Status != "failed" && p.Status != "needs_attention" && p.Status != "blocked" {
			return domain.ErrConflict
		}
		if p.StartedAt == nil {
			state = "on_hold"
		} else {
			state = "retry_wait"
		}
		cancel = false
	case "publish-now":
		if p.StartedAt != nil || p.Status == "published" || p.Status == "cancelled" {
			return domain.ErrConflict
		}
		at = now
		state = "on_hold"
		if p.Status == "scheduled" || p.Status == "queued" || p.Status == "retry_wait" || p.Status == "blocked" {
			state = "scheduled"
		}
		cancel = false
	case "cancel":
		if p.Status == "published" {
			return fmt.Errorf("%w: already published; automatic deletion is unsupported", domain.ErrConflict)
		}
		cancel = true
		if p.StartedAt == nil {
			state = "cancelled"
		} else {
			state = "cancel_pending"
		}
	default:
		return fmt.Errorf("%w: unknown publication action", domain.ErrInvalid)
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET status=$2,cancel_requested=$3,publish_at=$4,version=version+1,next_attempt_at=$5,error=NULL,legacy_status=CASE WHEN $6 IN ('approve','retry') THEN NULL ELSE legacy_status END WHERE id=$1`, id, state, cancel, at, now, action); err != nil {
		return err
	}
	if p.StartedAt == nil {
		if err = manualPublication(ctx, tx, id); err != nil {
			return err
		}
	}
	if p.StartedAt == nil || action == "cancel" {
		if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='superseded',lease_token=NULL,lease_until=NULL WHERE publication_id=$1 AND status='active'`, id); err != nil {
			return err
		}
	}
	if err = insertEvent(ctx, tx, id, "", action, state, map[string]any{"publish_at": at}); err != nil {
		return err
	}
	if action != "cancel" {
		if err = enforceTwitchCaps(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func insertEvent(ctx context.Context, tx pgx.Tx, id int64, attempt, kind, state string, detail any) error {
	_, err := tx.Exec(ctx, `INSERT INTO publication_events(id,publication_id,attempt_id,kind,state,detail)VALUES($1,$2,NULLIF($3,''),$4,$5,$6)`, uuid.NewString(), id, attempt, kind, state, mustJSON(detail))
	return err
}
func (s *Store) PublicationEvents(ctx context.Context, id int64) ([]domain.PublicationEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,kind,state,detail,created_at,attempt_id FROM publication_events WHERE publication_id=$1 ORDER BY created_at,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PublicationEvent{}
	for rows.Next() {
		var e domain.PublicationEvent
		if err = rows.Scan(&e.ID, &e.Kind, &e.State, &e.Detail, &e.CreatedAt, &e.AttemptID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) PreparePublications(ctx context.Context, now time.Time, limit int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publications p SET status='blocked',error=CASE WHEN p.platform<>'youtube' THEN 'Publishing for this platform is not implemented' ELSE 'Account OAuth connection is required' END FROM accounts a WHERE a.id=p.account_id AND p.status IN ('scheduled','retry_wait') AND p.publish_at<=$1 AND (p.platform<>'youtube' OR a.connection_state<>'connected')`, now); err != nil {
		return err
	}
	// A queued attempt also has a lease deadline: lost queue delivery is recovered here.
	if _, err = tx.Exec(ctx, `UPDATE publications p SET status=CASE WHEN p.cancel_requested THEN 'cancel_pending' ELSE 'retry_wait' END,next_attempt_at=$1
      FROM publication_attempts a WHERE a.publication_id=p.id AND a.status='active' AND a.lease_until<$1 AND a.publication_version=p.version AND p.status NOT IN ('published','cancelled','on_hold','needs_attention')`, now); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='expired',updated_at=$1 WHERE status='active' AND lease_until<$1`, now); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT p.id,p.version,p.status FROM publications p JOIN clip_revisions r ON r.id=p.revision_id JOIN accounts a ON a.id=p.account_id
      WHERE p.status IN ('scheduled','retry_wait','processing','ready','publishing','cancel_pending') AND p.next_attempt_at<=$1 AND (p.publish_at<=$1 OR p.cancel_requested)
      AND p.platform='youtube' AND (p.cancel_requested OR (r.render_state='ready' AND r.approved_at IS NOT NULL AND a.connection_state='connected'))
      AND NOT EXISTS(SELECT 1 FROM publication_attempts x WHERE x.publication_id=p.id AND x.status='active')
      ORDER BY p.publish_at LIMIT $2 FOR UPDATE OF p SKIP LOCKED`, now, limit)
	if err != nil {
		return err
	}
	type due struct {
		id      int64
		version int
		state   string
	}
	var ds []due
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.id, &d.version, &d.state); err != nil {
			rows.Close()
			return err
		}
		ds = append(ds, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range ds {
		if allowed, e := flowPublicationAllowed(ctx, tx, d.id); e != nil {
			return e
		} else if !allowed {
			continue
		}
		if ok, e := twitchRuntimeCapacity(ctx, tx, d.id, now); e != nil {
			return e
		} else if !ok {
			continue
		}
		attempt := uuid.NewString()
		_, err = tx.Exec(ctx, `INSERT INTO publication_attempts(id,publication_id,publication_version,phase,lease_until,checkpoint)
        VALUES($1,$2,$3,$4,$5,COALESCE((SELECT checkpoint FROM publication_attempts WHERE publication_id=$2 ORDER BY created_at DESC LIMIT 1),'{}'::jsonb))`, attempt, d.id, d.version, d.state, now.Add(2*time.Minute))
		if err != nil {
			return err
		}
		payload := map[string]any{"kind": "publish", "publication_id": d.id, "attempt_id": attempt, "expected_version": d.version}
		if _, err = tx.Exec(ctx, `INSERT INTO publication_outbox(id,publication_id,kind,payload)VALUES($1,$2,'publish',$3)`, uuid.NewString(), d.id, mustJSON(payload)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE publications SET status='queued' WHERE id=$1`, d.id); err != nil {
			return err
		}
	}
	// Render commands use the same outbox and survive worker/queue interruption.
	if _, err = tx.Exec(ctx, `UPDATE render_attempts SET status='expired' WHERE status='active' AND lease_until<$1`, now); err != nil {
		return err
	}
	rr, err := tx.Query(ctx, `SELECT r.id,r.revision_no FROM clip_revisions r WHERE r.render_state IN ('pending','rendering') AND NOT EXISTS(SELECT 1 FROM render_attempts a WHERE a.revision_id=r.id AND a.status='active') ORDER BY r.id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return err
	}
	type renderDue struct {
		id      int64
		version int
	}
	var renders []renderDue
	for rr.Next() {
		var r renderDue
		if err = rr.Scan(&r.id, &r.version); err != nil {
			rr.Close()
			return err
		}
		renders = append(renders, r)
	}
	err = rr.Err()
	rr.Close()
	if err != nil {
		return err
	}
	for _, r := range renders {
		attempt := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO render_attempts(id,revision_id,lease_until)VALUES($1,$2,$3)`, attempt, r.id, now.Add(2*time.Minute)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO publication_outbox(id,kind,payload)VALUES($1,'render_revision',$2)`, uuid.NewString(), mustJSON(map[string]any{"kind": "render", "revision_id": r.id, "attempt_id": attempt, "expected_version": r.version})); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func manualPublication(ctx context.Context, tx pgx.Tx, id int64) error {
	if _, err := tx.Exec(ctx, `UPDATE twitch_outputs SET auto_eligible=FALSE WHERE clip_id=(SELECT clip_id FROM publications WHERE id=$1)`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE publications SET auto_flow_id=NULL,auto_hold=FALSE WHERE id=$1 AND started_at IS NULL`, id)
	return err
}
func (s *Store) PendingOutbox(ctx context.Context, now time.Time, limit int) ([]domain.OutboxRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,kind,payload FROM publication_outbox WHERE delivered_at IS NULL AND next_delivery_at<=$1 ORDER BY created_at LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.OutboxRecord{}
	for rows.Next() {
		var o domain.OutboxRecord
		if err = rows.Scan(&o.ID, &o.Kind, &o.Payload); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) MarkOutbox(ctx context.Context, id string, sendErr error) error {
	var err error
	if sendErr == nil {
		_, err = s.pool.Exec(ctx, `UPDATE publication_outbox SET delivered_at=now(),attempts=attempts+1,error=NULL WHERE id=$1`, id)
	} else {
		_, err = s.pool.Exec(ctx, `UPDATE publication_outbox SET attempts=attempts+1,next_delivery_at=now()+interval '15 seconds',error=$2 WHERE id=$1`, id, sendErr.Error())
	}
	return err
}

func (s *Store) PutOAuthState(ctx context.Context, state string, id int64, redirect string, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO oauth_sessions(state,account_id,redirect_uri,expires_at)VALUES($1,$2,$3,$4)`, state, id, redirect, expires)
	return err
}
func (s *Store) ConsumeOAuthState(ctx context.Context, state string, now time.Time) (int64, string, error) {
	var id int64
	var redirect string
	err := s.pool.QueryRow(ctx, `UPDATE oauth_sessions SET used_at=$2 WHERE state=$1 AND used_at IS NULL AND expires_at>$2 RETURNING account_id,redirect_uri`, state, now).Scan(&id, &redirect)
	return id, redirect, mapErr(err)
}
func (s *Store) SetAccountConnection(ctx context.Context, id int64, state, remote string, ref json.RawMessage, scopes []string, expires *time.Time, errorMsg string) error {
	if len(ref) == 0 {
		ref = json.RawMessage(`{}`)
	}
	if scopes == nil {
		scopes = []string{}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE accounts SET connection_state=$2,remote_account_id=NULLIF($3,''),credential_ref=$4,connection_scopes=$5,credential_expires_at=$6,connection_error=NULLIF($7,''),connection_checked_at=now(),connection_version=connection_version+1 WHERE id=$1`, id, state, remote, ref, scopes, expires, errorMsg)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	if state == "connected" {
		if _, err = tx.Exec(ctx, `UPDATE publications SET status=CASE WHEN started_at IS NULL THEN 'scheduled' ELSE 'retry_wait' END,error=NULL,next_attempt_at=now() WHERE account_id=$1 AND status='blocked' AND platform='youtube'`, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) CredentialRef(ctx context.Context, id int64) (json.RawMessage, error) {
	var ref json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT credential_ref FROM accounts WHERE id=$1`, id).Scan(&ref)
	return ref, mapErr(err)
}
func (s *Store) ConfirmAccountConnection(ctx context.Context, id int64, version int, remote string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE accounts SET connection_state='connected',connection_version=connection_version+1 WHERE id=$1 AND connection_version=$2 AND remote_account_id=$3 AND connection_state='pending_confirmation'`, id, version, remote)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE publications SET status=CASE WHEN started_at IS NULL THEN 'scheduled' ELSE 'retry_wait' END,error=NULL,next_attempt_at=now() WHERE account_id=$1 AND status='blocked' AND platform='youtube'`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
