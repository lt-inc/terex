package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"terex/proto/twitch"
	"time"
)

// This queue receives coordinator grants; quotas are not independently counted.
func (s *Store) QueueAdmissions(ctx context.Context, grants []twitch.DownloadGrant, now time.Time) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(846923102)`); err != nil {
		return 0, err
	}
	added := 0
	for _, g := range grants {
		var epoch int64
		if err = tx.QueryRow(ctx, `SELECT epoch FROM twitch_collector.state WHERE id=1`).Scan(&epoch); err != nil {
			return 0, err
		}
		if g.Epoch != epoch {
			continue
		}
		// An admission is not an upload generation. Keep its source and identity
		// durably so a failure before ReserveImport can still release preparation.
		reservation, _ := json.Marshal(twitch.Import{ImportID: g.ImportID, Source: g.Source, FlowID: g.FlowID, FlowVersion: g.FlowVersion, PreparationFormat: g.Format, Epoch: g.Epoch, AdmissionSequence: g.AdmissionSequence, RevalidationToken: g.RevalidationToken})
		var state, failureToken, acceptedToken string
		var old []byte
		e := tx.QueryRow(ctx, `SELECT state,COALESCE(reservation,'{}'::jsonb),failure_token,revalidation_token FROM twitch_collector.download_tasks WHERE clip_id=$1 FOR UPDATE`, g.ClipID).Scan(&state, &old, &failureToken, &acceptedToken)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return 0, e
		}
		var previous twitch.Import
		if len(old) > 0 {
			if e = json.Unmarshal(old, &previous); e != nil {
				return 0, e
			}
		}
		if state != "" && (previous.Epoch != g.Epoch || g.AdmissionSequence < previous.AdmissionSequence) {
			continue
		}
		if previous.ImportID != "" && previous.ImportID != g.ImportID {
			continue
		}
		if state != "" && g.AdmissionSequence == previous.AdmissionSequence && (previous.FlowID != "" && previous.FlowID != g.FlowID || g.FlowVersion < previous.FlowVersion) {
			continue
		}
		if state != "" && g.AdmissionSequence == previous.AdmissionSequence && previous.Source.ObservedAt.After(g.Source.ObservedAt) {
			var admitted twitch.Import
			_ = json.Unmarshal(reservation, &admitted)
			admitted.Source = previous.Source
			reservation, _ = json.Marshal(admitted)
		}
		recovered := (state == "failed" && failureToken != "" && g.RevalidationToken == failureToken || state == "discovered" && acceptedToken != "" && g.RevalidationToken == acceptedToken) && g.AdmissionSequence > previous.AdmissionSequence && previous.Epoch == g.Epoch && previous.ImportID == g.ImportID
		if state != "" && !(state == "queued" && previous.UploadGeneration == 0 || recovered) {
			continue
		}
		tag, e := tx.Exec(ctx, `UPDATE twitch_collector.clips SET state='queued',reserved_at=$2,last_error='',error_kind='' WHERE clip_id=$1 AND (state IN('discovered','queued') OR $3)`, g.ClipID, now, recovered)
		if e != nil {
			return 0, e
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_collector.download_tasks(clip_id,run_at,flow_id,flow_version,reservation,epoch,dispatch_state,grant_checked_at,grant_expires_at)VALUES($1,$2,$3,$4,$5,$6,'authorized',$2,$9)ON CONFLICT(clip_id)DO UPDATE SET dispatch_entered_at=CASE WHEN twitch_collector.download_tasks.dispatch_state<>'authorized' THEN now() ELSE twitch_collector.download_tasks.dispatch_entered_at END,transfer_phase='',dispatch_state='authorized',dispatch_reason_code='',dispatch_reason='',grant_checked_at=$2,grant_expires_at=$9,state='queued',attempts=CASE WHEN $7 THEN 0 ELSE twitch_collector.download_tasks.attempts END,run_at=CASE WHEN $7 THEN EXCLUDED.run_at ELSE twitch_collector.download_tasks.run_at END,flow_id=EXCLUDED.flow_id,flow_version=EXCLUDED.flow_version,reservation=EXCLUDED.reservation,epoch=EXCLUDED.epoch,last_error=CASE WHEN $7 THEN '' ELSE twitch_collector.download_tasks.last_error END,error_code=CASE WHEN $7 THEN '' ELSE twitch_collector.download_tasks.error_code END,uploaded=NULL,failure_synced_at=NULL,lease_token=NULL,lease_expires_at=NULL,revalidation_token=CASE WHEN $7 THEN $8 ELSE twitch_collector.download_tasks.revalidation_token END WHERE twitch_collector.download_tasks.state IN('queued','failed','discovered') AND twitch_collector.download_tasks.uploaded IS NULL AND COALESCE((twitch_collector.download_tasks.reservation->>'upload_generation')::bigint,0)=0`, g.ClipID, now, g.FlowID, g.FlowVersion, reservation, g.Epoch, recovered, g.RevalidationToken, g.ExpiresAt); err != nil {
			return 0, err
		}
		added++
	}
	return added, tx.Commit(ctx)
}
