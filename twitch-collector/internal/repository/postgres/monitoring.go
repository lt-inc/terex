package postgres

import (
	"context"
	"terex/proto/twitch"
)

func (s *Store) RuntimeWork(ctx context.Context) (out twitch.RuntimeWork, err error) {
	out.Counts = []twitch.StageCount{}
	out.ActiveTasks = []twitch.ActiveTask{}
	if err = s.pool.QueryRow(ctx, `SELECT epoch FROM twitch_collector.state WHERE id=1`).Scan(&out.Epoch); err != nil {
		return
	}
	rows, e := s.pool.Query(ctx, `SELECT CASE WHEN t.dispatch_state='verification_pending' THEN 'verification' ELSE 'download' END,CASE WHEN t.state='leased' AND t.lease_expires_at>now() THEN c.state WHEN t.state IN('queued','leased') THEN t.dispatch_state ELSE c.state END,'source',count(*)::integer FROM twitch_collector.download_tasks t JOIN twitch_collector.clips c ON c.clip_id=t.clip_id WHERE t.epoch=$1 AND t.state NOT IN('done') GROUP BY 1,2`, out.Epoch)
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
	rows, err = s.pool.Query(ctx, `SELECT c.clip_id,c.title,t.flow_id,COALESCE(f.config->>'name',t.flow_id),COALESCE(t.reservation->>'preparation_format','source'),
 CASE WHEN t.dispatch_state='verification_pending' THEN 'verification' ELSE 'download' END,CASE WHEN t.state='leased' AND t.lease_expires_at>now() THEN c.state ELSE t.dispatch_state END,CASE WHEN t.dispatch_state='verification_pending' THEN t.verification_attempts ELSE t.attempts END,CASE WHEN t.state='leased' AND t.lease_expires_at>now() THEN t.started_at END,t.updated_at,t.run_at,c.error_kind,c.last_error,
 'https://clips.twitch.tv/'||c.clip_id,'/admin/twitch/clips?flow='||t.flow_id,count(*) OVER()::integer
 FROM twitch_collector.download_tasks t JOIN twitch_collector.clips c ON c.clip_id=t.clip_id LEFT JOIN twitch_collector.flows f ON f.id=t.flow_id
 WHERE t.epoch=$1 AND t.state IN('queued','leased') AND(t.dispatch_state<>'waiting_assignment' OR(t.state='leased' AND t.lease_expires_at>now())) ORDER BY (t.state='leased' AND t.lease_expires_at>now()) DESC,t.updated_at DESC,c.clip_id LIMIT 100`, out.Epoch)
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
