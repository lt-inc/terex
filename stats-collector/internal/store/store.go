// Package store keeps YouTube statistics snapshots in the yt_stats schema.
// Grafana reads these tables directly; accounts are read from public.accounts.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"terex/proto/pgxutil"
	"terex/proto/workflow"
)

type Store struct{ pool *pgxpool.Pool }

func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxutil.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err = pgxutil.WaitPing(ctx, p.Ping); err != nil {
		p.Close()
		return nil, err
	}
	return &Store{pool: p}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Exec runs a statement; used by tests to prepare data.
func (s *Store) Exec(ctx context.Context, sql string) error {
	_, err := s.pool.Exec(ctx, sql)
	return err
}

const migration = `
CREATE SCHEMA IF NOT EXISTS yt_stats;
CREATE TABLE IF NOT EXISTS yt_stats.channels(
 channel_id text PRIMARY KEY, account_id bigint NOT NULL, account_name text NOT NULL,
 title text NOT NULL DEFAULT '', custom_url text NOT NULL DEFAULT '', thumbnail_url text NOT NULL DEFAULT '',
 uploads_playlist text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS yt_stats.channel_snapshots(
 channel_id text NOT NULL REFERENCES yt_stats.channels(channel_id) ON DELETE CASCADE, ts timestamptz NOT NULL,
 subscribers bigint, views bigint NOT NULL, videos integer NOT NULL, PRIMARY KEY(channel_id, ts));
CREATE TABLE IF NOT EXISTS yt_stats.videos(
 video_id text PRIMARY KEY, channel_id text NOT NULL REFERENCES yt_stats.channels(channel_id) ON DELETE CASCADE,
 title text NOT NULL, thumbnail_url text NOT NULL DEFAULT '', published_at timestamptz NOT NULL, duration_sec integer NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS videos_channel ON yt_stats.videos(channel_id, published_at DESC);
CREATE TABLE IF NOT EXISTS yt_stats.video_snapshots(
 video_id text NOT NULL REFERENCES yt_stats.videos(video_id) ON DELETE CASCADE, ts timestamptz NOT NULL,
 views bigint NOT NULL, likes bigint, comments bigint, PRIMARY KEY(video_id, ts));
CREATE INDEX IF NOT EXISTS video_snapshots_ts ON yt_stats.video_snapshots(ts);
CREATE TABLE IF NOT EXISTS yt_stats.collect_runs(
 id bigserial PRIMARY KEY, started_at timestamptz NOT NULL, finished_at timestamptz NOT NULL,
 channels integer NOT NULL DEFAULT 0, videos integer NOT NULL DEFAULT 0, quota_units integer NOT NULL DEFAULT 0, error text NOT NULL DEFAULT '');
`

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(846923102)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, migration); err != nil {
		return fmt.Errorf("stats migration: %w", err)
	}
	return tx.Commit(ctx)
}

type Account struct {
	ID            int64
	Name          string
	ChannelID     string
	CredentialRef workflow.ObjectRef
}

// Accounts returns connected YouTube accounts: the ones gpu-be holds a credential for.
// An account that needs re-auth is skipped until it is reconnected; its history is kept.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, remote_account_id, credential_ref->>'bucket', credential_ref->>'object' FROM public.accounts
		WHERE platform='youtube' AND connection_state='connected' AND COALESCE(remote_account_id,'')<>''
		AND COALESCE(credential_ref->>'object','')<>'' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err = rows.Scan(&a.ID, &a.Name, &a.ChannelID, &a.CredentialRef.Bucket, &a.CredentialRef.Object); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SaveChannel upserts the channel and appends a snapshot.
func (s *Store) SaveChannel(ctx context.Context, a Account, c workflow.ChannelStats, ts time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO yt_stats.channels(channel_id,account_id,account_name,title,custom_url,thumbnail_url,uploads_playlist,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,now())
		ON CONFLICT(channel_id) DO UPDATE SET account_id=$2,account_name=$3,title=$4,custom_url=$5,thumbnail_url=$6,uploads_playlist=$7,updated_at=now()`,
		c.ChannelID, a.ID, a.Name, c.Title, c.CustomURL, c.ThumbnailURL, ""); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO yt_stats.channel_snapshots(channel_id,ts,subscribers,views,videos) VALUES($1,$2,$3,$4,$5)
		ON CONFLICT DO NOTHING`, c.ChannelID, ts, c.Subscribers, c.Views, c.Videos); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SaveVideos upserts video metadata and appends one snapshot per video.
func (s *Store) SaveVideos(ctx context.Context, channelID string, vs []workflow.VideoStats, ts time.Time) error {
	if len(vs) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, v := range vs {
		b.Queue(`INSERT INTO yt_stats.videos(video_id,channel_id,title,thumbnail_url,published_at,duration_sec) VALUES($1,$2,$3,$4,$5,$6)
			ON CONFLICT(video_id) DO UPDATE SET title=$3,thumbnail_url=$4,duration_sec=$6`,
			v.ID, channelID, v.Title, v.ThumbnailURL, v.PublishedAt, v.DurationSec)
		b.Queue(`INSERT INTO yt_stats.video_snapshots(video_id,ts,views,likes,comments) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
			v.ID, ts, v.Views, v.Likes, v.Comments)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	res := tx.SendBatch(ctx, b)
	for range b.Len() {
		if _, err = res.Exec(); err != nil {
			_ = res.Close()
			return err
		}
	}
	if err = res.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DropRemoved deletes channels whose account no longer exists or lost its channel id.
func (s *Store) DropRemoved(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM yt_stats.channels c WHERE NOT EXISTS (
		SELECT 1 FROM public.accounts a WHERE a.platform='youtube' AND a.remote_account_id=c.channel_id)`)
	return tag.RowsAffected(), err
}

// Thin keeps every snapshot for fullDays, then one (the last) per UTC day.
func (s *Store) Thin(ctx context.Context, fullDays int) (int64, error) {
	var total int64
	for _, q := range []string{
		`DELETE FROM yt_stats.video_snapshots s USING (
		   SELECT video_id, ts, row_number() OVER (PARTITION BY video_id, date_trunc('day', ts) ORDER BY ts DESC) rn
		   FROM yt_stats.video_snapshots WHERE ts < now() - make_interval(days => $1)) d
		 WHERE s.video_id=d.video_id AND s.ts=d.ts AND d.rn>1`,
		`DELETE FROM yt_stats.channel_snapshots s USING (
		   SELECT channel_id, ts, row_number() OVER (PARTITION BY channel_id, date_trunc('day', ts) ORDER BY ts DESC) rn
		   FROM yt_stats.channel_snapshots WHERE ts < now() - make_interval(days => $1)) d
		 WHERE s.channel_id=d.channel_id AND s.ts=d.ts AND d.rn>1`,
	} {
		tag, err := s.pool.Exec(ctx, q, fullDays)
		if err != nil {
			return total, err
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

type Run struct {
	Started, Finished time.Time
	Channels, Videos  int
	Units             int
	Err               string
}

func (s *Store) SaveRun(ctx context.Context, r Run) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO yt_stats.collect_runs(started_at,finished_at,channels,videos,quota_units,error) VALUES($1,$2,$3,$4,$5,$6)`,
		r.Started, r.Finished, r.Channels, r.Videos, r.Units, r.Err)
	if err == nil {
		_, err = s.pool.Exec(ctx, `DELETE FROM yt_stats.collect_runs WHERE started_at < now() - interval '30 days'`)
	}
	return err
}
