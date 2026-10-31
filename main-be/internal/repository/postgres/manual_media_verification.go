package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"terex/main-be/internal/domain"
	"terex/proto/workflow"
)

func publicationMediaToVerifyTx(ctx context.Context, tx pgx.Tx, clip string, q domain.VerifyPublicationMediaRequest) (manualMedia, error) {
	var rid int64
	if err := tx.QueryRow(ctx, `SELECT latest_revision_id FROM clips WHERE clip_id=$1 FOR UPDATE`, clip).Scan(&rid); err != nil {
		return manualMedia{}, mapErr(err)
	}
	r, err := scanRevision(tx.QueryRow(ctx, `SELECT `+revisionCols+` FROM clip_revisions WHERE id=$1`, rid))
	if err != nil {
		return manualMedia{}, err
	}
	if r.ID != q.RevisionID || r.Version != q.Version || q.MediaKind != "rendered" {
		return manualMedia{}, domain.ErrConflict
	}
	return manualMediaTx(ctx, tx, r, q.MediaKind)
}

func (s *Store) PublicationMediaForVerification(ctx context.Context, clip string, q domain.VerifyPublicationMediaRequest) (workflow.ObjectRef, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return workflow.ObjectRef{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return workflow.ObjectRef{}, err
	}
	m, err := publicationMediaToVerifyTx(ctx, tx, clip, q)
	return m.ref, err
}

func (s *Store) RecordPublicationMediaVerification(ctx context.Context, clip string, q domain.VerifyPublicationMediaRequest, ref workflow.ObjectRef, proof workflow.MediaDigest) error {
	if !validMediaSHA(proof.SHA256) || proof.SizeBytes <= 0 || proof.SizeBytes > workflow.MaxPublicationMediaBytes {
		return fmt.Errorf("%w: Не удалось подтвердить целостность видео", domain.ErrConflict)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockTwitch(ctx, tx); err != nil {
		return err
	}
	m, err := publicationMediaToVerifyTx(ctx, tx, clip, q)
	if err != nil {
		return err
	}
	if m.ref != ref || (validMediaSHA(m.sha) && m.sha != proof.SHA256) || (m.size > 0 && m.size != proof.SizeBytes) {
		return fmt.Errorf("%w: Файл изменился; проверьте версию клипа", domain.ErrConflict)
	}
	// Enrich the same ready revision. Do not approve it, change its media/editor
	// version, retarget any queued publication, or create queue work.
	if _, err = tx.Exec(ctx, `UPDATE clip_revisions SET media_sha256=$2,media_size_bytes=$3 WHERE id=$1`, q.RevisionID, proof.SHA256, proof.SizeBytes); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
