package postgres

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

const discoveryMigration = `CREATE TABLE IF NOT EXISTS twitch_collector.discovery_progress(flow_id text NOT NULL,target text NOT NULL,epoch bigint NOT NULL,flow_version integer NOT NULL,progress jsonb NOT NULL,updated_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(flow_id,target));`

func (s *Store) LoadDiscoveryProgress(ctx context.Context, flow, target string, epoch int64, version int) (twitch.DiscoveryProgress, error) {
	var b []byte
	var out twitch.DiscoveryProgress
	err := s.pool.QueryRow(ctx, `SELECT progress FROM twitch_collector.discovery_progress WHERE flow_id=$1 AND target=$2 AND epoch=$3 AND flow_version=$4`, flow, target, epoch, version).Scan(&b)
	if err == pgx.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}
func (s *Store) SaveDiscoveryProgress(ctx context.Context, flow, target string, p twitch.DiscoveryProgress) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(846923102)`); err != nil {
		return err
	}
	var epoch int64
	if err = tx.QueryRow(ctx, `SELECT epoch FROM twitch_collector.state WHERE id=1 FOR SHARE`).Scan(&epoch); err != nil {
		return err
	}
	if epoch != p.Epoch {
		return domain.ErrLeaseLost
	}
	var enabled bool
	var version int
	if err = tx.QueryRow(ctx, `SELECT enabled,COALESCE((config->>'version')::int,0) FROM twitch_collector.flows WHERE id=$1 FOR SHARE`, flow).Scan(&enabled, &version); err != nil {
		return err
	}
	if !enabled || version != p.FlowVersion {
		return domain.ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO twitch_collector.discovery_progress(flow_id,target,epoch,flow_version,progress) VALUES($1,$2,$3,$4,$5) ON CONFLICT(flow_id,target) DO UPDATE SET epoch=excluded.epoch,flow_version=excluded.flow_version,progress=excluded.progress,updated_at=now()`, flow, target, p.Epoch, p.FlowVersion, b)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
