package postgres

import (
	"context"
	"encoding/json"
	"terex/twitch-collector/internal/domain"
)

// Terminal failures must release their coordinator reservation even after a
// restart or a lost abort response. The local task remains failed for review.
func (s *Store) FailedImports(ctx context.Context, epoch int64) ([]domain.FailedImport, error) {
	rows, err := s.pool.Query(ctx, `SELECT clip_id,reservation,last_error,updated_at FROM twitch_collector.download_tasks
WHERE state='failed' AND epoch=$1 AND flow_id<>'' AND failure_synced_at IS NULL
AND COALESCE(reservation->>'import_id','')<>'' ORDER BY updated_at,clip_id LIMIT 20`, epoch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.FailedImport{}
	for rows.Next() {
		var failure domain.FailedImport
		var raw []byte
		if err = rows.Scan(&failure.ClipID, &raw, &failure.Reason, &failure.FailedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &failure.Reservation); err != nil {
			return nil, err
		}
		out = append(out, failure)
	}
	return out, rows.Err()
}

func (s *Store) FailedImportSynced(ctx context.Context, failure domain.FailedImport) error {
	_, err := s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks SET failure_synced_at=now()
WHERE clip_id=$1 AND state='failed' AND epoch=$2 AND reservation->>'import_id'=$3
AND COALESCE((reservation->>'upload_generation')::bigint,0)=$4 AND updated_at=$5`, failure.ClipID, failure.Reservation.Epoch, failure.Reservation.ImportID, failure.Reservation.UploadGeneration, failure.FailedAt)
	return err
}
