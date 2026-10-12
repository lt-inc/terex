package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"terex/proto/twitch"
)

// A control read is a single database snapshot, never a cache refresh that
// accidentally gives an old reset epoch a new freshness timestamp.
func (s *Store) TwitchCollectorConfig(ctx context.Context) (out twitch.CollectorConfig, err error) {
	started := time.Now()
	phase := "pool"
	var poolMS int64
	defer func() {
		slog.InfoContext(ctx, "collector config snapshot", "phase", phase, "pool_wait_ms", poolMS,
			"duration_ms", time.Since(started).Milliseconds(), "flows", len(out.Flows), "blocked_sources", len(out.BlockedSourceIDs), "err", err)
	}()
	conn, err := s.controlDB().Acquire(ctx)
	poolMS = time.Since(started).Milliseconds()
	if err != nil {
		return out, err
	}
	defer conn.Release()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	phase = "settings"
	out.Settings, err = readTwitchSettings(ctx, tx)
	if err != nil {
		return out, err
	}
	phase = "flows"
	rows, err := tx.Query(ctx, `SELECT `+collectorFlowCols+` FROM twitch_flows WHERE NOT archived ORDER BY created_at,id`)
	if err != nil {
		return out, err
	}
	out.Flows = []twitch.Flow{}
	for rows.Next() {
		flow, e := scanFlow(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		if flow.BlockReason != "" {
			flow.Enabled = false
		}
		out.Flows = append(out.Flows, flow)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	phase = "reset_fence"
	control, err := readTwitchResetControl(ctx, tx)
	if err != nil {
		return out, err
	}
	out.Epoch = control.Epoch
	if control.State != "idle" && control.State != "completed" {
		out.Maintenance = &twitch.ResetMaintenance{ID: control.ID, Epoch: control.Epoch, State: control.State}
	}
	phase = "blocked_sources"
	rows, err = tx.Query(ctx, `SELECT twitch_clip_id FROM twitch_imports WHERE rejected OR media_state='expired' ORDER BY twitch_clip_id`)
	if err != nil {
		return out, err
	}
	out.BlockedSourceIDs = []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		out.BlockedSourceIDs = append(out.BlockedSourceIDs, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	phase = "commit"
	err = tx.Commit(ctx)
	return out, err
}

func readTwitchResetControl(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (out twitch.ResetStatus, err error) {
	var active bool
	err = q.QueryRow(ctx, `SELECT c.epoch,c.active,COALESCE(c.reset_id,''),COALESCE(r.state,'idle')
 FROM twitch_reset_control c LEFT JOIN twitch_reset_runs r ON r.id=c.reset_id WHERE c.id=1`).Scan(&out.Epoch, &active, &out.ID, &out.State)
	if err == nil && active && (out.ID == "" || out.State == "idle" || out.State == "completed") {
		return out, fmt.Errorf("active Twitch reset has an inconsistent durable state")
	}
	if !active && out.State != "completed" {
		out.State = "idle"
	}
	out.Blockers = []twitch.ResetBlocker{}
	return out, err
}

// Idle reset synchronization must not aggregate the historical deletion graph.
func (s *Store) TwitchResetControl(ctx context.Context) (twitch.ResetStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return readTwitchResetControl(ctx, s.controlDB())
}
