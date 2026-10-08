package postgres

import (
	"context"
	"encoding/json"
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
		reservation, _ := json.Marshal(twitch.Import{PreparationFormat: g.Format, Epoch: g.Epoch})
		tag, e := tx.Exec(ctx, `UPDATE twitch_collector.clips SET state='queued',reserved_at=$2,last_error='',error_kind='' WHERE clip_id=$1 AND state IN('discovered','queued')`, g.ClipID, now)
		if e != nil {
			return 0, e
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		if _, err = tx.Exec(ctx, `INSERT INTO twitch_collector.download_tasks(clip_id,run_at,flow_id,flow_version,reservation,epoch)VALUES($1,$2,$3,$4,$5,$6)ON CONFLICT(clip_id)DO UPDATE SET flow_id=EXCLUDED.flow_id,flow_version=EXCLUDED.flow_version,reservation=EXCLUDED.reservation,epoch=EXCLUDED.epoch WHERE twitch_collector.download_tasks.state='queued' AND COALESCE(twitch_collector.download_tasks.reservation->>'import_id','')=''`, g.ClipID, now, g.FlowID, g.FlowVersion, reservation, g.Epoch); err != nil {
			return 0, err
		}
		added++
	}
	return added, tx.Commit(ctx)
}
