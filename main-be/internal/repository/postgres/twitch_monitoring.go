package postgres

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"terex/proto/twitch"
	"time"
)

func (s *Store) TwitchRuntimeWork(ctx context.Context, now time.Time) (out twitch.RuntimeWork, err error) {
	out.Counts = []twitch.StageCount{}
	out.ActiveTasks = []twitch.ActiveTask{}
	var active bool
	err = s.pool.QueryRow(ctx, `SELECT epoch,active FROM twitch_reset_control WHERE id=1`).Scan(&out.Epoch, &active)
	if err != nil {
		return
	}
	if active {
		err = waitPreparation("twitch_reset", "Снимок мониторинга ожидает окончания сброса", now)
		return
	}
	err = s.pool.QueryRow(ctx, `SELECT min(a.created_at) FROM render_attempts a JOIN clip_revisions r ON r.id=a.revision_id JOIN twitch_outputs o ON o.clip_id=r.clip_id WHERE a.status='active' AND a.execution_started_at IS NULL AND a.dispatch_deadline>$1`, now).Scan(&out.OldestRenderQueuedAt)
	if err != nil {
		return
	}
	rows, e := s.pool.Query(ctx, `SELECT 'import',state,'source',count(*)::integer FROM twitch_imports GROUP BY state
 UNION ALL SELECT CASE WHEN o.format='compilation' THEN 'compilation' ELSE 'render' END,
 CASE WHEN EXISTS(SELECT 1 FROM render_attempts a WHERE a.revision_id=r.id AND a.status='active' AND a.execution_started_at IS NOT NULL AND a.lease_until>now()) THEN 'rendering' WHEN EXISTS(SELECT 1 FROM render_attempts a WHERE a.revision_id=r.id AND a.status='active' AND a.execution_started_at IS NULL AND a.dispatch_deadline>now()) THEN 'queued' WHEN r.render_state='rendering' THEN 'pending' ELSE r.render_state END,o.format,count(*)::integer
 FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id GROUP BY 1,2,3
 UNION ALL SELECT 'publication',CASE WHEN p.status='uploading' AND NOT EXISTS(SELECT 1 FROM publication_attempts a WHERE a.publication_id=p.id AND a.publication_version=p.version AND a.status='active' AND a.lease_until>now()) THEN 'needs_attention' ELSE p.status END,o.format,count(*)::integer FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id GROUP BY 2,3`)
	if e != nil {
		err = e
		return
	}
	for rows.Next() {
		var v twitch.StageCount
		if err = rows.Scan(&v.Stage, &v.State, &v.Format, &v.Count); err != nil {
			rows.Close()
			return
		}
		out.Counts = append(out.Counts, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return
	}
	// Lateral lease selection cannot duplicate a current revision/publication.
	// A worker whose lease expired is shown as queued, never as still rendering.
	rows, err = s.pool.Query(ctx, `WITH tasks AS (
 SELECT c.clip_id,c.title,o.flow_id,COALESCE(f.config->>'name',o.flow_id) flow_name,o.format,
 CASE WHEN o.format='compilation' THEN 'compilation' ELSE 'render' END stage,
 CASE WHEN a.id IS NULL THEN 'pending' WHEN a.execution_started_at IS NULL THEN 'queued' ELSE 'rendering' END state,0 attempt,a.execution_started_at started_at,r.created_at updated_at,
 NULL::timestamptz next_attempt_at,'' error_kind,COALESCE(r.error,'') error,COALESCE(i.request->'source'->>'url','') source_url,
 '/admin/clips/'||c.clip_id terex_url
 FROM twitch_outputs o JOIN clips c ON c.clip_id=o.clip_id JOIN clip_revisions r ON r.id=c.latest_revision_id
 JOIN twitch_flows f ON f.id=o.flow_id LEFT JOIN twitch_imports i ON i.id=o.source_id
 LEFT JOIN LATERAL(SELECT id,created_at,execution_started_at FROM render_attempts WHERE revision_id=r.id AND status='active' AND ((execution_started_at IS NOT NULL AND lease_until>$1) OR (execution_started_at IS NULL AND dispatch_deadline>$1)) ORDER BY created_at DESC LIMIT 1)a ON true
 WHERE r.render_state IN('pending','rendering','queued')
 UNION ALL
 SELECT c.clip_id,c.title,o.flow_id,COALESCE(f.config->>'name',o.flow_id),o.format,'publication',CASE WHEN p.status='uploading' AND a.created_at IS NULL THEN 'needs_attention' ELSE p.status END,
 (SELECT count(*)::integer FROM publication_attempts x WHERE x.publication_id=p.id),a.created_at,COALESCE(a.updated_at,p.started_at,p.next_attempt_at),p.next_attempt_at,'',COALESCE(p.error,''),COALESCE(i.request->'source'->>'url',''),'/admin/clips/'||c.clip_id
 FROM publications p JOIN twitch_outputs o ON o.clip_id=p.clip_id JOIN clips c ON c.clip_id=p.clip_id JOIN twitch_flows f ON f.id=o.flow_id
 LEFT JOIN twitch_imports i ON i.id=o.source_id
 LEFT JOIN LATERAL(SELECT created_at,updated_at FROM publication_attempts WHERE publication_id=p.id AND publication_version=p.version AND status='active' AND lease_until>$1 ORDER BY created_at DESC LIMIT 1)a ON true
 WHERE a.created_at IS NOT NULL OR p.status IN('scheduled','queued','retry_wait','uploading','uploaded','processing','verifying','needs_attention')
 ) SELECT *,count(*) OVER()::integer FROM tasks ORDER BY (started_at IS NOT NULL) DESC,updated_at DESC,clip_id LIMIT 100`, now)
	if err != nil {
		return
	}
	for rows.Next() {
		var v twitch.ActiveTask
		if err = rows.Scan(&v.ClipID, &v.Title, &v.FlowID, &v.FlowName, &v.Format, &v.Stage, &v.State, &v.Attempt, &v.StartedAt, &v.UpdatedAt, &v.NextAttemptAt, &v.ErrorKind, &v.Error, &v.SourceURL, &v.TerexURL, &out.ActiveTotal); err != nil {
			rows.Close()
			return
		}
		out.ActiveTasks = append(out.ActiveTasks, v)
	}
	rows.Close()
	err = rows.Err()
	return
}

func (s *Store) SaveTwitchMonitoring(ctx context.Context, scope, key string, epoch int64, at time.Time, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockMonitoringEpoch(ctx, tx, epoch); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO twitch_monitoring_snapshots(scope,key,epoch,generated_at,payload)VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(scope,key) DO UPDATE SET epoch=EXCLUDED.epoch,generated_at=EXCLUDED.generated_at,payload=EXCLUDED.payload WHERE twitch_monitoring_snapshots.generated_at<=EXCLUDED.generated_at`, scope, key, epoch, at, raw)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CleanupTwitchMonitoring(ctx context.Context, epoch int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockMonitoringEpoch(ctx, tx, epoch); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM twitch_monitoring_snapshots WHERE epoch<>$1 OR(scope='flow' AND NOT EXISTS(SELECT 1 FROM twitch_flows f WHERE f.id=key AND NOT f.archived))`, epoch)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Monitoring changes only disposable snapshots. A shared reset-control row lock
// excludes epoch changes without queueing behind expensive work admission on the
// global Twitch advisory lock. Reset updates this row before removing snapshots.
func lockMonitoringEpoch(ctx context.Context, tx pgx.Tx, epoch int64) error {
	var current int64
	var active bool
	if err := tx.QueryRow(ctx, `SELECT epoch,active FROM twitch_reset_control WHERE id=1 FOR SHARE`).Scan(&current, &active); err != nil {
		return err
	}
	if active || current != epoch {
		return waitPreparation("twitch_reset", "Снимок мониторинга ожидает окончания сброса или нового расчёта", time.Now().UTC())
	}
	return nil
}
