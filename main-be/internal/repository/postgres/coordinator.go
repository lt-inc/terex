package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"terex/proto/workflow"
)

const leaseDuration = 2 * time.Minute

func (s *Store) ClaimWorkflow(ctx context.Context, q *workflow.ClaimRequest, now time.Time) (*workflow.Lease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return nil, err
	}
	out := &workflow.Lease{Granted: false, AttemptID: q.AttemptID, ExpectedVersion: q.ExpectedVersion}
	token := uuid.NewString()
	until := now.Add(leaseDuration)
	if q.Kind == workflow.KindRender {
		var rid int64
		var status, worker string
		var expiry time.Time
		err = tx.QueryRow(ctx, `SELECT revision_id,status,worker_id,lease_until FROM render_attempts WHERE id=$1 FOR UPDATE`, q.AttemptID).Scan(&rid, &status, &worker, &expiry)
		if err == pgx.ErrNoRows {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if rid != q.RevisionID || status != "active" || worker != "" || expiry.Before(now) {
			return out, nil
		}
		r, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1 FOR UPDATE`, rid))
		if err != nil {
			return nil, err
		}
		if r.Version != q.ExpectedVersion || r.RenderState == "ready" || r.RenderState == "failed" {
			return out, nil
		}
		var jobID string
		if err = tx.QueryRow(ctx, `SELECT job_id FROM clips WHERE clip_id=$1`, r.ClipID).Scan(&jobID); err != nil {
			return nil, err
		}
		var source workflow.SourceManifest
		if err = json.Unmarshal(r.Manifest, &source); err != nil {
			return nil, err
		}
		cues := make([]workflow.Cue, len(r.Cues))
		for i, c := range r.Cues {
			cues[i] = workflow.Cue{ID: c.ID, Start: c.Start, End: c.End, Text: c.Text}
		}
		source.Options.Subtitles = r.SubtitlesEnabled
		out.Render = &workflow.RenderWork{RevisionID: rid, ClipID: r.ClipID, JobID: jobID, StartSec: r.StartSec, EndSec: r.EndSec, Cues: cues, Source: source, Output: workflow.ObjectRef{Bucket: "clips", Object: r.ClipID + "/revision-" + jsonNumber(rid) + "/" + q.AttemptID + ".mp4"}}
		if _, err = tx.Exec(ctx, `UPDATE render_attempts SET worker_id=$2,lease_token=$3,lease_until=$4 WHERE id=$1`, q.AttemptID, q.WorkerID, token, until); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET render_state='rendering' WHERE id=$1`, rid); err != nil {
			return nil, err
		}
	} else if q.Kind == workflow.KindPublish {
		var pid int64
		var version int
		var status, worker, phase string
		var expiry time.Time
		var checkpoint []byte
		err = tx.QueryRow(ctx, `SELECT publication_id,publication_version,status,worker_id,phase,COALESCE(lease_until,'epoch'::timestamptz),checkpoint FROM publication_attempts WHERE id=$1 FOR UPDATE`, q.AttemptID).Scan(&pid, &version, &status, &worker, &phase, &expiry, &checkpoint)
		if err == pgx.ErrNoRows {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if pid != q.PublicationID || version != q.ExpectedVersion || status != "active" || worker != "" || expiry.Before(now) {
			return out, nil
		}
		p, err := scanPublication(tx.QueryRow(ctx, `SELECT `+publicationCols+` FROM publications p JOIN accounts a ON a.id=p.account_id WHERE p.id=$1 FOR UPDATE OF p`, pid))
		if err != nil {
			return nil, err
		}
		if p.Version != version || p.Status != "queued" {
			return out, nil
		}
		if p.StartedAt == nil {
			if allowed, e := twitchRuntimeCapacity(ctx, tx, pid, now); e != nil {
				return nil, e
			} else if !allowed {
				return out, nil
			}
		}
		if err = requireTwitchSources(ctx, tx, p.ClipID, p.RevisionID); err != nil && !p.CancelRequested {
			return out, nil
		}
		r, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, p.RevisionID))
		if err != nil {
			return nil, err
		}
		var remote, connection string
		var credential []byte
		if err = tx.QueryRow(ctx, `SELECT COALESCE(remote_account_id,''),connection_state,credential_ref FROM accounts WHERE id=$1`, p.AccountID).Scan(&remote, &connection, &credential); err != nil {
			return nil, err
		}
		var ref workflow.ObjectRef
		_ = json.Unmarshal(credential, &ref)
		var cp workflow.Checkpoint
		if err = json.Unmarshal(checkpoint, &cp); err != nil {
			return nil, err
		}
		if p.UploadID != nil && cp.RemoteID == "" {
			cp.RemoteID = *p.UploadID
		}
		if p.ExternalURL != nil && cp.RemoteURL == "" {
			cp.RemoteURL = *p.ExternalURL
		}
		if p.CancelRequested {
			phase = "cancel_pending"
		} else if connection != "connected" || ref.Object == "" {
			if _, err = tx.Exec(ctx, `UPDATE publications SET status='blocked',error='Account OAuth connection is required' WHERE id=$1`, pid); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='failed',error='Account OAuth connection is required' WHERE id=$1`, q.AttemptID); err != nil {
				return nil, err
			}
			if err = tx.Commit(ctx); err != nil {
				return nil, err
			}
			return out, nil
		}
		if !p.CancelRequested && (r.ApprovedAt == nil || r.RenderState != "ready") {
			return out, nil
		}
		meta := workflow.Metadata{Title: p.Title, Description: p.Description, Hashtags: p.Hashtags, CategoryID: p.CategoryID, MadeForKids: p.MadeForKids}
		visibility := p.Visibility
		if visibility == "" {
			visibility = "public"
		}
		out.Publication = &workflow.PublicationWork{PublicationID: pid, RevisionID: r.ID, ClipID: p.ClipID, AccountID: p.AccountID, Provider: p.Platform, RemoteAccountID: remote, CredentialRef: ref, Media: workflow.ObjectRef{Bucket: r.Bucket, Object: r.Object}, Metadata: meta, Visibility: visibility, State: phase, PublishAt: p.PublishAt, Checkpoint: cp}
		if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET worker_id=$2,lease_token=$3,lease_until=$4 WHERE id=$1`, q.AttemptID, q.WorkerID, token, until); err != nil {
			return nil, err
		}
		state := phase
		if state == "scheduled" || state == "retry_wait" {
			state = "uploading"
		}
		if _, err = tx.Exec(ctx, `UPDATE publications SET status=$2,started_at=COALESCE(started_at,$3) WHERE id=$1`, pid, state, now); err != nil {
			return nil, err
		}
		if err = insertEvent(ctx, tx, pid, q.AttemptID, "claimed", state, map[string]any{"worker": q.WorkerID}); err != nil {
			return nil, err
		}
	} else {
		return out, nil
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	out.Granted = true
	out.LeaseToken = token
	out.ExpiresAt = until
	return out, nil
}
func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }

// lockAttempt checks the fencing token and the publication version in the same transaction.
func lockAttempt(ctx context.Context, tx pgx.Tx, q workflow.LeaseRequest, now time.Time) (int64, bool, error) {
	var pid int64
	var version int
	var token, status string
	var until time.Time
	err := tx.QueryRow(ctx, `SELECT publication_id,publication_version,COALESCE(lease_token,''),status,COALESCE(lease_until,'epoch'::timestamptz) FROM publication_attempts WHERE id=$1 FOR UPDATE`, q.AttemptID).Scan(&pid, &version, &token, &status, &until)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if token != q.LeaseToken || q.LeaseToken == "" || version != q.ExpectedVersion || status != "active" || until.Before(now) {
		return pid, false, nil
	}
	var current int
	err = tx.QueryRow(ctx, `SELECT version FROM publications WHERE id=$1 FOR UPDATE`, pid).Scan(&current)
	return pid, err == nil && current == version, err
}
func lockRender(ctx context.Context, tx pgx.Tx, q workflow.LeaseRequest, now time.Time) (int64, bool, error) {
	var rid int64
	var token, status string
	var until time.Time
	err := tx.QueryRow(ctx, `SELECT revision_id,COALESCE(lease_token,''),status,lease_until FROM render_attempts WHERE id=$1 FOR UPDATE`, q.AttemptID).Scan(&rid, &token, &status, &until)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if token != q.LeaseToken || q.LeaseToken == "" || status != "active" || until.Before(now) {
		return rid, false, nil
	}
	var version int
	err = tx.QueryRow(ctx, `SELECT revision_no FROM clip_revisions WHERE id=$1 FOR UPDATE`, rid).Scan(&version)
	return rid, err == nil && version == q.ExpectedVersion, err
}
func (s *Store) RenewWorkflow(ctx context.Context, q *workflow.LeaseRequest, now time.Time) (*workflow.Permit, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	out := &workflow.Permit{}
	pid, ok, err := lockAttempt(ctx, tx, *q, now)
	if err != nil {
		return nil, err
	}
	until := now.Add(leaseDuration)
	if ok {
		var cancel, approved bool
		if err = tx.QueryRow(ctx, `SELECT p.cancel_requested,r.approved_at IS NOT NULL,p.status FROM publications p JOIN clip_revisions r ON r.id=p.revision_id WHERE p.id=$1`, pid).Scan(&cancel, &approved, &out.State); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET lease_until=$2,updated_at=$3 WHERE id=$1`, q.AttemptID, until, now); err != nil {
			return nil, err
		}
		out.Allowed = true
		out.CanPublish = !cancel && approved
		out.ExpiresAt = until
	} else {
		_, rok, e := lockRender(ctx, tx, *q, now)
		if e != nil {
			return nil, e
		}
		if rok {
			if _, err = tx.Exec(ctx, `UPDATE render_attempts SET lease_until=$2 WHERE id=$1`, q.AttemptID, until); err != nil {
				return nil, err
			}
			out.Allowed = true
			out.ExpiresAt = until
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func persistCheckpoint(ctx context.Context, tx pgx.Tx, id string, pid int64, cp workflow.Checkpoint, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE publication_attempts SET checkpoint=$2,phase=CASE WHEN $3='' THEN phase ELSE $3 END,updated_at=$4 WHERE id=$1`, id, mustJSON(cp), cp.State, now); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE publications SET status=CASE WHEN $2='' THEN status ELSE $2 END,upload_id=COALESCE(NULLIF($3,''),upload_id),external_url=COALESCE(NULLIF($4,''),external_url),uploaded_at=COALESCE($5,uploaded_at),processed_at=COALESCE($6,processed_at),last_verified_at=COALESCE($7,last_verified_at) WHERE id=$1`, pid, cp.State, cp.RemoteID, cp.RemoteURL, cp.UploadCompletedAt, cp.ProcessingCompletedAt, cp.VerifiedAt)
	return err
}
func (s *Store) CheckpointWorkflow(ctx context.Context, q *workflow.CheckpointRequest, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	pid, ok, err := lockAttempt(ctx, tx, q.LeaseRequest, now)
	if err != nil || !ok {
		return false, err
	}
	var previousPhase string
	if err = tx.QueryRow(ctx, `SELECT phase FROM publication_attempts WHERE id=$1`, q.AttemptID).Scan(&previousPhase); err != nil {
		return false, err
	}
	if err = persistCheckpoint(ctx, tx, q.AttemptID, pid, q.Checkpoint, now); err != nil {
		return false, err
	}
	if q.State != "" && q.State != previousPhase {
		if err = insertEvent(ctx, tx, pid, q.AttemptID, "checkpoint", q.State, map[string]any{"confirmed_bytes": q.ConfirmedBytes, "total_bytes": q.TotalBytes, "remote_id": q.RemoteID, "uploaded_at": q.UploadCompletedAt, "processed_at": q.ProcessingCompletedAt}); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
func (s *Store) CompleteWorkflow(ctx context.Context, q *workflow.CompleteRequest, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	pid, ok, err := lockAttempt(ctx, tx, q.LeaseRequest, now)
	if err != nil {
		return false, err
	}
	if ok {
		if err = persistCheckpoint(ctx, tx, q.AttemptID, pid, q.Checkpoint, now); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='completed',lease_until=NULL,updated_at=$2 WHERE id=$1`, q.AttemptID, now); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE publications SET status=$2,published_at=CASE WHEN $2='published' THEN COALESCE(published_at,$3) ELSE published_at END,next_attempt_at=$3+interval '15 seconds',error=NULL WHERE id=$1`, pid, q.State, now); err != nil {
			return false, err
		}
		if err = insertEvent(ctx, tx, pid, q.AttemptID, "confirmed", q.State, map[string]any{"remote_id": q.RemoteID, "remote_url": q.RemoteURL, "uploaded_at": q.UploadCompletedAt, "processed_at": q.ProcessingCompletedAt, "verified_at": q.VerifiedAt}); err != nil {
			return false, err
		}
	} else {
		rid, rok, e := lockRender(ctx, tx, q.LeaseRequest, now)
		if e != nil || !rok {
			return false, e
		}
		if q.Media.Bucket == "" || q.Media.Object == "" {
			return false, nil
		}
		if q.Source != nil {
			if math.IsNaN(q.Source.DurationSec) || math.IsInf(q.Source.DurationSec, 0) || q.Source.DurationSec <= 0 || q.Source.Original.Bucket != "imports" || q.Source.Original.Object == "" {
				return false, fmt.Errorf("invalid resolved assembly source")
			}
			var old workflow.SourceManifest
			var data []byte
			if err = tx.QueryRow(ctx, `SELECT manifest FROM clip_revisions WHERE id=$1`, rid).Scan(&data); err != nil {
				return false, err
			}
			if err = json.Unmarshal(data, &old); err != nil {
				return false, err
			}
			if old.Assembly == nil || q.Source.Assembly == nil || string(mustJSON(old.Assembly)) != string(mustJSON(q.Source.Assembly)) {
				return false, fmt.Errorf("assembly specification changed during rendering")
			}
			if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET manifest=$2,spec=spec||jsonb_build_object('start_sec',0,'end_sec',$3::double precision) WHERE id=$1`, rid, mustJSON(q.Source), q.Source.DurationSec); err != nil {
				return false, err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET render_state='ready',bucket=$2,object=$3,error=NULL WHERE id=$1`, rid, q.Media.Bucket, q.Media.Object); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE render_attempts SET status='completed',lease_token=NULL WHERE id=$1`, q.AttemptID); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE clips c SET bucket=$2,object=$3,start_sec=(r.spec->>'start_sec')::double precision,end_sec=(r.spec->>'end_sec')::double precision FROM clip_revisions r WHERE r.id=$1 AND c.latest_revision_id=r.id`, rid, q.Media.Bucket, q.Media.Object); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE twitch_imports i SET state='ready',error=NULL,updated_at=now() FROM clips c WHERE c.clip_id=i.clip_id AND c.latest_revision_id=$1`, rid); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE jobs j SET status='done',fail_place='',updated_at=now() FROM clips c WHERE c.job_id=j.job_id AND c.latest_revision_id=$1 AND j.source_kind IN('twitch','twitch_compilation')`, rid); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
func (s *Store) FailWorkflow(ctx context.Context, q *workflow.FailRequest, now time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	pid, ok, err := lockAttempt(ctx, tx, q.LeaseRequest, now)
	if err != nil {
		return false, err
	}
	if ok {
		state := q.State
		delay := q.RetryAfterSeconds
		if delay < 5 {
			delay = 15
		}
		if q.Retryable {
			state = "retry_wait"
		}
		if state == "" {
			state = "failed"
		}
		if q.Retryable {
			var failures int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM publication_attempts WHERE publication_id=$1 AND publication_version=$2 AND status='failed'`, pid, q.ExpectedVersion).Scan(&failures); err != nil {
				return false, err
			}
			if failures >= 4 {
				state = "needs_attention"
			}
			backoff := 15 * (1 << min(failures, 6))
			if delay < backoff {
				delay = backoff
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE publication_attempts SET status='failed',error=$2,updated_at=$3 WHERE id=$1`, q.AttemptID, q.Message, now); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE publications SET status=$2,error=$3,next_attempt_at=$4 WHERE id=$1`, pid, state, q.Message, now.Add(time.Duration(delay)*time.Second)); err != nil {
			return false, err
		}
		if q.ReconnectRequired {
			if _, err = tx.Exec(ctx, `UPDATE accounts SET connection_state='needs_reauth',connection_error=$2,connection_checked_at=$3 WHERE id=(SELECT account_id FROM publications WHERE id=$1)`, pid, q.Message, now); err != nil {
				return false, err
			}
		}
		if err = insertEvent(ctx, tx, pid, q.AttemptID, "failed", state, map[string]any{"code": q.Code, "message": q.Message, "retryable": q.Retryable}); err != nil {
			return false, err
		}
	} else {
		rid, rok, e := lockRender(ctx, tx, q.LeaseRequest, now)
		if e != nil || !rok {
			return false, e
		}
		state := "failed"
		if q.Retryable {
			state = "pending"
		}
		if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET render_state=$2,error=$3 WHERE id=$1`, rid, state, q.Message); err != nil {
			return false, err
		}
		importState, jobState := "failed", "failed"
		if q.Retryable {
			importState, jobState = "rendering", "processing"
		}
		if _, err = tx.Exec(ctx, `UPDATE twitch_imports i SET state=$3,error=$2,updated_at=now() FROM clips c WHERE c.clip_id=i.clip_id AND c.latest_revision_id=$1`, rid, q.Message, importState); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE jobs j SET status=$2,fail_place='render_revision',updated_at=now() FROM clips c WHERE c.job_id=j.job_id AND c.latest_revision_id=$1 AND j.source_kind IN('twitch','twitch_compilation')`, rid, jobState); err != nil {
			return false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE render_attempts SET status='failed' WHERE id=$1`, q.AttemptID); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
