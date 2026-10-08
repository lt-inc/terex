package postgres

import "context"

const resetMigration = `
ALTER TABLE twitch_collector.state ADD COLUMN IF NOT EXISTS epoch bigint NOT NULL DEFAULT 0;
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS epoch bigint NOT NULL DEFAULT 0;
`

func (s *Store) ResetTwitchPool(ctx context.Context, epoch int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(846923101)`); err != nil {
		return err
	}
	var current int64
	if err = tx.QueryRow(ctx, `SELECT epoch FROM twitch_collector.state WHERE id=1 FOR UPDATE`).Scan(&current); err != nil {
		return err
	}
	if epoch <= current {
		return tx.Commit(ctx)
	}
	for _, q := range []string{`DELETE FROM twitch_collector.download_tasks`, `DELETE FROM twitch_collector.consumers`, `DELETE FROM twitch_collector.clips`, `UPDATE twitch_collector.flows SET enabled=false`, `UPDATE twitch_collector.channels SET last_checked_at=NULL,last_error=''`} {
		if _, err = tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE twitch_collector.state SET epoch=$1,last_check_at=NULL,last_error='',next_flow_index=0 WHERE id=1`, epoch)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
