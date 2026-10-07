package postgres

import (
	"context"
	"terex/twitch-collector/internal/domain"
)

func (s *Store) ClipStatuses(ctx context.Context, flow string, ids []string) ([]domain.ClipStatus, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.clip_id,CASE WHEN c.state='queued' AND c.error_kind='capacity' THEN 'waiting_capacity' WHEN c.state='queued' AND c.error_kind='quota' THEN 'waiting_quota' WHEN c.state='queued' AND c.last_error<>'' THEN 'retry_wait' ELSE c.state END,c.last_error,CASE WHEN t.state='queued' THEN t.run_at END,
 CASE WHEN t.state='leased' THEN t.started_at END,COALESCE(t.attempts,0)
 FROM twitch_collector.clips c JOIN twitch_collector.consumers co ON co.clip_id=c.clip_id
 LEFT JOIN twitch_collector.download_tasks t ON t.clip_id=c.clip_id WHERE co.flow_id=$1 AND c.clip_id=ANY($2) ORDER BY c.clip_id`, flow, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ClipStatus{}
	for rows.Next() {
		var item domain.ClipStatus
		if err = rows.Scan(&item.ClipID, &item.State, &item.Reason, &item.NextAttemptAt, &item.StartedAt, &item.Attempt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) SetTaskStage(ctx context.Context, t domain.Task, stage string) error {
	if stage != "importing" && stage != "verifying" {
		return domain.ErrConflict
	}
	tag, err := s.pool.Exec(ctx, `UPDATE twitch_collector.clips c SET state=$3 FROM twitch_collector.download_tasks t WHERE c.clip_id=t.clip_id AND `+validLease, t.Clip.ID, t.Token, stage)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	return nil
}

// Filter lookup returns only identifiers and states. Main-be applies its own
// search and output filters before paginating; videos and signed URLs stay out.
func (s *Store) FilteredClipStatuses(ctx context.Context, flow, state string) ([]domain.ClipStatus, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.clip_id,c.state FROM twitch_collector.clips c JOIN twitch_collector.consumers co ON co.clip_id=c.clip_id
 WHERE co.flow_id=$1 AND NOT co.excluded AND CASE $2
 WHEN 'failed' THEN c.state='failed' OR(c.state='queued' AND c.last_error<>'' AND c.error_kind NOT IN('quota','capacity'))
 WHEN 'retry_wait' THEN c.state='queued' AND c.last_error<>'' AND c.error_kind NOT IN('quota','capacity')
 WHEN 'waiting_quota' THEN c.state='queued' AND c.error_kind='quota'
 WHEN 'waiting_capacity' THEN c.state='queued' AND c.error_kind='capacity'
 WHEN 'queued' THEN c.state='queued' AND c.last_error=''
 WHEN 'downloading' THEN c.state IN('downloading','importing','verifying')
 WHEN 'importing' THEN c.state IN('importing','verifying')
 WHEN 'verifying' THEN c.state='verifying'
 ELSE false END ORDER BY c.clip_id LIMIT 10001`, flow, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.ClipStatus{}
	for rows.Next() {
		var item domain.ClipStatus
		if err = rows.Scan(&item.ClipID, &item.State); err != nil {
			return nil, err
		}
		out = append(out, item)
		if len(out) > 10000 {
			return nil, &domain.Failure{Kind: "status_limit", Message: "Too many matching source states; narrow the source filter"}
		}
	}
	return out, rows.Err()
}
