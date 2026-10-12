package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"terex/main-be/internal/domain"
)

const channelCols = "id, name, url, yt_channel_id, approve_mode, enabled, spec, last_checked_at, feed_error_code, last_feed_error, feed_failure_count, next_feed_check_at"

func scanChannel(r pgx.Row) (domain.Channel, error) {
	var c domain.Channel
	var spec []byte
	err := r.Scan(&c.ID, &c.Name, &c.URL, &c.YTChannelID, &c.ApproveMode, &c.Enabled, &spec, &c.LastCheckedAt, &c.FeedErrorCode, &c.LastFeedError, &c.FeedFailureCount, &c.NextFeedCheckAt)
	if err == nil && len(spec) > 0 {
		err = json.Unmarshal(spec, &c.Spec)
	}
	return c, err
}

func (s *Store) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+channelCols+" FROM channels ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetChannel(ctx context.Context, id int64) (domain.Channel, error) {
	c, err := scanChannel(s.pool.QueryRow(ctx, "SELECT "+channelCols+" FROM channels WHERE id=$1", id))
	return c, mapErr(err)
}

func (s *Store) CreateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error) {
	spec, _ := json.Marshal(c.Spec)
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO channels(name, url, yt_channel_id, approve_mode, enabled, spec)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`, c.Name, c.URL, c.YTChannelID, c.ApproveMode, c.Enabled, spec).Scan(&id)
	if err != nil {
		return domain.Channel{}, err
	}
	return s.GetChannel(ctx, id)
}

func (s *Store) UpdateChannel(ctx context.Context, id int64, u domain.ChannelUpdate) (domain.Channel, error) {
	var spec []byte
	if u.Spec != nil {
		spec, _ = json.Marshal(u.Spec)
	}
	_, err := s.pool.Exec(ctx, `UPDATE channels SET name=COALESCE($2,name), approve_mode=COALESCE($3,approve_mode),
		enabled=COALESCE($4,enabled), spec=COALESCE($5::jsonb,spec),
		next_feed_check_at=CASE WHEN $4::boolean IS TRUE THEN NULL ELSE next_feed_check_at END WHERE id=$1`, id, u.Name, u.ApproveMode, u.Enabled, spec)
	if err != nil {
		return domain.Channel{}, err
	}
	return s.GetChannel(ctx, id)
}

func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM channels WHERE id=$1", id)
	return err
}

// MarkSeen inserts ids and returns those that were new.
func (s *Store) MarkSeen(ctx context.Context, channelID int64, ids []string) ([]string, error) {
	var fresh []string
	for _, id := range ids {
		tag, err := s.pool.Exec(ctx, "INSERT INTO seen_videos(channel_id, video_id) VALUES ($1,$2) ON CONFLICT DO NOTHING", channelID, id)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 1 {
			fresh = append(fresh, id)
		}
	}
	return fresh, nil
}

func (s *Store) TouchChannel(ctx context.Context, id int64, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE channels SET last_checked_at=$2,
		feed_error_code='',last_feed_error='',feed_failure_count=0,next_feed_check_at=NULL WHERE id=$1`, id, at)
	return err
}

func (s *Store) RecordChannelFeedFailure(ctx context.Context, id int64, code, message string, nextCheck time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE channels SET feed_error_code=$2,last_feed_error=$3,
		feed_failure_count=feed_failure_count+1,next_feed_check_at=$4 WHERE id=$1`, id, code, message, nextCheck)
	return err
}
