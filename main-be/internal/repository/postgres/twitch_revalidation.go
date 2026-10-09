package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/twitch"
)

// A receipt outlives the failed task and its grant. A repeated request must not
// reset a newer grant if the first response was lost between the two databases.
func (s *Store) RevalidateFlowImport(ctx context.Context, id string, q twitch.RevalidateImportRequest, now time.Time) (twitch.RevalidationReceipt, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return twitch.RevalidationReceipt{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return twitch.RevalidationReceipt{}, err
	}
	if err = twitchResetRequestAllowed(ctx, tx, q.Epoch); err != nil {
		return twitch.RevalidationReceipt{}, err
	}
	out, err := importByID(ctx, tx, id)
	if err != nil {
		return twitch.RevalidationReceipt{}, err
	}
	if out.Epoch != q.Epoch {
		return twitch.RevalidationReceipt{}, domain.ErrConflict
	}
	var marker []byte
	if err = tx.QueryRow(ctx, `SELECT COALESCE(revalidation,'{}'::jsonb) FROM twitch_imports WHERE id=$1 FOR UPDATE`, id).Scan(&marker); err != nil {
		return twitch.RevalidationReceipt{}, err
	}
	var saved struct {
		Receipts map[string]twitch.RevalidationReceipt `json:"receipts"`
	}
	if err = json.Unmarshal(marker, &saved); err != nil {
		return twitch.RevalidationReceipt{}, err
	}
	if receipt, ok := saved.Receipts[q.FailureToken]; ok {
		if receipt.Epoch != q.Epoch || receipt.AdmissionSequence != q.AdmissionSequence || receipt.UploadGeneration != q.UploadGeneration {
			return receipt, domain.ErrConflict
		}
		return receipt, tx.Commit(ctx)
	}
	if out.UploadGeneration != q.UploadGeneration || out.Rejected || out.MediaState == "expired" {
		return twitch.RevalidationReceipt{}, domain.ErrConflict
	}
	if !q.Manual && (q.Reason != "flow_version_changed" || q.UploadGeneration != 0) {
		return twitch.RevalidationReceipt{}, domain.ErrConflict
	}
	var sequence int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT sequence FROM twitch_download_grants WHERE import_id=$1),0)`, id).Scan(&sequence); err != nil {
		return twitch.RevalidationReceipt{}, err
	}
	// Legacy rows had no sequence in their persisted reservation. They can be
	// repaired once; subsequent attempts must name the coordinator's sequence.
	legacy := q.AdmissionSequence == 0 && len(saved.Receipts) == 0 && out.AdmissionSequence == 0
	if !legacy && sequence != q.AdmissionSequence {
		return twitch.RevalidationReceipt{}, domain.ErrConflict
	}
	f, err := flowByID(ctx, tx, q.FlowID)
	if err != nil {
		return twitch.RevalidationReceipt{}, err
	}
	if !f.Enabled {
		return twitch.RevalidationReceipt{}, waitPreparation("flow_paused", "Поток приостановлен; исходник сохранён до возобновления", now.Add(time.Minute))
	}
	if f.Version != q.FlowVersion {
		return twitch.RevalidationReceipt{}, waitPreparation("flow_version_changed", "Настройки потока изменились; повторная проверка будет выполнена с текущей версией", now.Add(time.Minute))
	}
	if err = confirmedFlow(ctx, tx, f); err != nil {
		return twitch.RevalidationReceipt{}, err
	}
	var excluded bool
	if err = tx.QueryRow(ctx, `SELECT excluded FROM twitch_flow_sources WHERE flow_id=$1 AND import_id=$2`, f.ID, id).Scan(&excluded); err != nil {
		return twitch.RevalidationReceipt{}, mapErr(err)
	}
	if excluded || !sourceMatches(f, out.Source) {
		return twitch.RevalidationReceipt{}, waitPreparation("source_filtered", "Исходник исключён или не соответствует языку и источникам потока", now.Add(time.Minute))
	}
	if out.Source.CreatedAt.Before(now.Add(-time.Duration(f.WindowHours)*time.Hour)) || !preparationSourceFresh(f, out.Source, now, now) {
		return twitch.RevalidationReceipt{}, waitPreparation("source_stale", "Клип устарел для текущего окна и повторно не скачивается", now.Add(time.Minute))
	}
	if !sourceEligible(f, out.Source, now) {
		return twitch.RevalidationReceipt{}, waitPreparation("source_filtered", "Исходник не соответствует просмотрам, длительности или минимальному возрасту потока", now.Add(time.Minute))
	}
	receipt := twitch.RevalidationReceipt{Epoch: q.Epoch, AdmissionSequence: q.AdmissionSequence, UploadGeneration: q.UploadGeneration, FailureToken: q.FailureToken, FlowID: f.ID, FlowVersion: f.Version, State: "discovered"}
	if out.MediaState == "ready" {
		receipt.State = "ready"
	} else if q.Uploaded {
		if q.UploadGeneration <= 0 || out.Original.Object == "" || out.State != "failed" {
			return receipt, domain.ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE twitch_imports SET state='upload_pending',media_state='pending',error=NULL,upload_claim_until=$2,updated_at=$3 WHERE id=$1`, id, now.Add(15*time.Minute), now); err != nil {
			return receipt, err
		}
		if _, err = tx.Exec(ctx, `UPDATE twitch_download_grants SET state='consumed' WHERE import_id=$1 AND sequence=$2 AND epoch=$3`, id, sequence, q.Epoch); err != nil {
			return receipt, err
		}
		receipt.State = "verification_retry"
	} else {
		if out.UploadGeneration > 0 && out.State != "failed" {
			return receipt, waitPreparation("upload_active", "Текущая загрузка ещё не завершена; её нельзя заменить повторной проверкой", now.Add(time.Minute))
		}
		// Failed/pre-upload tasks have no running collector lease. Changing state
		// fences a late Complete until the next Reserve issues a new generation.
		if _, err = tx.Exec(ctx, `UPDATE twitch_imports SET state='discovered',media_state='pending',error=NULL,original='{}',upload_claim_until=NULL,upload_expires_at=NULL,download_reserved_at=NULL,updated_at=$2 WHERE id=$1`, id, now); err != nil {
			return receipt, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM twitch_preparation_sources WHERE import_id=$1 AND reservation_id IN(SELECT id FROM twitch_preparation_reservations WHERE state='active')`, id); err != nil {
			return receipt, err
		}
		if _, err = tx.Exec(ctx, `UPDATE twitch_download_grants SET state='released' WHERE import_id=$1`, id); err != nil {
			return receipt, err
		}
		if _, err = tx.Exec(ctx, `UPDATE twitch_preparation_reservations r SET state='released',reason='source_revalidated' WHERE r.state='active' AND NOT EXISTS(SELECT 1 FROM twitch_preparation_sources ps WHERE ps.reservation_id=r.id)`); err != nil {
			return receipt, err
		}
		if _, err = tx.Exec(ctx, `UPDATE twitch_flow_sources SET config=$3,flow_version=$4,reserved_at=NULL WHERE flow_id=$1 AND import_id=$2`, f.ID, id, mustJSON(f), f.Version); err != nil {
			return receipt, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE twitch_imports SET revalidation=COALESCE(revalidation,'{}'::jsonb)||jsonb_build_object('failure_token',$2::text,'receipt',$3::jsonb,'receipts',COALESCE(revalidation->'receipts','{}'::jsonb)||jsonb_build_object($2::text,$3::jsonb)) WHERE id=$1`, id, q.FailureToken, mustJSON(receipt)); err != nil {
		return receipt, err
	}
	return receipt, tx.Commit(ctx)
}

func (s *Store) AbortFlowImportFenced(ctx context.Context, id string, q twitch.AbortImportRequest, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	if err = twitchResetRequestAllowed(ctx, tx, q.Epoch); err != nil {
		return err
	}
	out, err := importByID(ctx, tx, id)
	if err != nil {
		return err
	}
	if out.UploadGeneration != q.UploadGeneration || out.Epoch != q.Epoch {
		return domain.ErrConflict
	}
	if out.MediaState == "ready" {
		return tx.Commit(ctx)
	}
	{
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT NOT (COALESCE(i.revalidation->'receipts','{}'::jsonb) ? $4::text) AND ($4::text<>'' OR i.revalidation IS NULL) AND CASE WHEN $2::bigint>0 THEN EXISTS(SELECT 1 FROM twitch_download_grants g WHERE g.import_id=i.id AND g.sequence=$2 AND g.state IN('active','consumed') AND g.epoch=$3) ELSE COALESCE((i.request->>'admission_sequence')::bigint,0)=0 AND ($5::bigint>0 OR i.revalidation IS NULL) END FROM twitch_imports i WHERE i.id=$1`, id, q.AdmissionSequence, q.Epoch, q.FailureToken, q.UploadGeneration).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return fmt.Errorf("%w: admission was released or replaced", domain.ErrConflict)
		}
	}
	if err = abortFlowImportTx(ctx, tx, id, q.UploadGeneration, q.Reason, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
