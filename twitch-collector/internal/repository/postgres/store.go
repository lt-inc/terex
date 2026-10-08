package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"terex/proto/pgxutil"
	"terex/twitch-collector/internal/domain"
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

const migration = `
CREATE TABLE IF NOT EXISTS twitch_collector.schema_version(version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS twitch_collector.channels(
 id text PRIMARY KEY, broadcaster_id text NOT NULL UNIQUE, login text NOT NULL, display_name text NOT NULL,
 paused boolean NOT NULL DEFAULT false, archived boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(), last_checked_at timestamptz, last_error text NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS twitch_collector.clips(
 clip_id text PRIMARY KEY, channel_id text NOT NULL REFERENCES twitch_collector.channels(id), broadcaster_id text NOT NULL,
 broadcaster_name text NOT NULL, title text NOT NULL, view_count integer NOT NULL, created_at timestamptz NOT NULL,
 duration double precision NOT NULL, video_id text NOT NULL DEFAULT '', vod_offset double precision,
 state text NOT NULL DEFAULT 'discovered', last_error text NOT NULL DEFAULT '', import_id text NOT NULL DEFAULT '', sha256 text NOT NULL DEFAULT '',
 observed_at timestamptz NOT NULL DEFAULT now(), reserved_at timestamptz
);
CREATE INDEX IF NOT EXISTS clips_recent ON twitch_collector.clips(created_at DESC);
CREATE INDEX IF NOT EXISTS clips_reservations ON twitch_collector.clips(reserved_at,channel_id);
CREATE INDEX IF NOT EXISTS clips_sha256 ON twitch_collector.clips(sha256) WHERE sha256 <> '';
CREATE TABLE IF NOT EXISTS twitch_collector.download_tasks(
 clip_id text PRIMARY KEY REFERENCES twitch_collector.clips(clip_id), state text NOT NULL DEFAULT 'queued',
 attempts integer NOT NULL DEFAULT 0, run_at timestamptz NOT NULL DEFAULT now(),
 lease_token text, lease_expires_at timestamptz, last_error text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS download_ready ON twitch_collector.download_tasks(run_at) WHERE state IN ('queued','leased');
CREATE TABLE IF NOT EXISTS twitch_collector.state(id integer PRIMARY KEY CHECK(id=1),last_check_at timestamptz,last_error text NOT NULL DEFAULT '');
INSERT INTO twitch_collector.state(id) VALUES(1) ON CONFLICT DO NOTHING;
INSERT INTO twitch_collector.schema_version(version) VALUES(1) ON CONFLICT DO NOTHING;
ALTER TABLE twitch_collector.clips ADD COLUMN IF NOT EXISTS game_id text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.clips ADD COLUMN IF NOT EXISTS language text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.clips ADD COLUMN IF NOT EXISTS language_source text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.clips ADD COLUMN IF NOT EXISTS thumbnail_url text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.clips ADD COLUMN IF NOT EXISTS error_kind text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS flow_id text NOT NULL DEFAULT '';
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS flow_version integer NOT NULL DEFAULT 0;
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS reservation jsonb;
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS started_at timestamptz;
ALTER TABLE twitch_collector.download_tasks ADD COLUMN IF NOT EXISTS failure_synced_at timestamptz;
CREATE TABLE IF NOT EXISTS twitch_collector.flows(id text PRIMARY KEY,enabled boolean NOT NULL,config jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS twitch_collector.consumers(clip_id text NOT NULL REFERENCES twitch_collector.clips(clip_id),flow_id text NOT NULL REFERENCES twitch_collector.flows(id),flow_version integer NOT NULL,eligible boolean NOT NULL DEFAULT true,PRIMARY KEY(clip_id,flow_id));
ALTER TABLE twitch_collector.consumers ADD COLUMN IF NOT EXISTS excluded boolean NOT NULL DEFAULT false;
ALTER TABLE twitch_collector.state ADD COLUMN IF NOT EXISTS next_flow_index integer NOT NULL DEFAULT 0;
ALTER TABLE twitch_collector.state ADD COLUMN IF NOT EXISTS legacy_broadcaster_ids text[];
UPDATE twitch_collector.state SET legacy_broadcaster_ids=COALESCE((SELECT array_agg(broadcaster_id ORDER BY broadcaster_id) FROM twitch_collector.channels WHERE NOT archived),ARRAY[]::text[]) WHERE legacy_broadcaster_ids IS NULL;
INSERT INTO twitch_collector.schema_version(version) VALUES(2) ON CONFLICT DO NOTHING;
`

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(846923101)"); err != nil {
		return err
	}
	// IF NOT EXISTS still requires CREATE on the database. The production
	// principal owns a provisioned schema and must not receive that privilege.
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='twitch_collector')").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err = tx.Exec(ctx, "CREATE SCHEMA twitch_collector"); err != nil {
			return fmt.Errorf("provision collector schema before startup: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, migration+resetMigration); err != nil {
		return fmt.Errorf("collector migration: %w", err)
	}
	return tx.Commit(ctx)
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func translate(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

const channelColumns = `id,broadcaster_id,login,display_name,paused,archived,created_at,last_checked_at,last_error`

func scanChannel(row pgx.Row) (c domain.Channel, err error) {
	err = row.Scan(&c.ID, &c.BroadcasterID, &c.Login, &c.DisplayName, &c.Paused, &c.Archived, &c.CreatedAt, &c.LastCheckedAt, &c.LastError)
	return c, translate(err)
}
func (s *Store) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+channelColumns+" FROM twitch_collector.channels ORDER BY archived,login")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Channel{}
	for rows.Next() {
		c, e := scanChannel(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *Store) CreateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Channel{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(846923103)"); err != nil {
		return domain.Channel{}, err
	}
	result, err := scanChannel(tx.QueryRow(ctx, `INSERT INTO twitch_collector.channels(id,broadcaster_id,login,display_name)
 VALUES($1,$2,$3,$4) ON CONFLICT(broadcaster_id) DO UPDATE SET login=excluded.login,display_name=excluded.display_name,archived=false
 RETURNING `+channelColumns, newID(), c.BroadcasterID, c.Login, c.DisplayName))
	if err != nil {
		return domain.Channel{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Channel{}, err
	}
	return result, nil
}
func (s *Store) GetChannel(ctx context.Context, id string) (domain.Channel, error) {
	return scanChannel(s.pool.QueryRow(ctx, "SELECT "+channelColumns+" FROM twitch_collector.channels WHERE id=$1", id))
}
func (s *Store) SetChannel(ctx context.Context, id string, paused *bool, archive bool) (domain.Channel, error) {
	return scanChannel(s.pool.QueryRow(ctx, `UPDATE twitch_collector.channels SET paused=COALESCE($2,paused),archived=CASE WHEN $3 THEN true ELSE archived END WHERE id=$1 RETURNING `+channelColumns, id, paused, archive))
}
func (s *Store) ChannelChecked(ctx context.Context, id string, at time.Time, message string) error {
	_, err := s.pool.Exec(ctx, "UPDATE twitch_collector.channels SET last_checked_at=CASE WHEN $3='' THEN $2 ELSE last_checked_at END,last_error=$3 WHERE id=$1", id, at, message)
	return err
}
func (s *Store) UpsertClips(ctx context.Context, clips []domain.Clip) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, c := range clips {
		_, err = tx.Exec(ctx, `INSERT INTO twitch_collector.clips(clip_id,channel_id,broadcaster_id,broadcaster_name,title,view_count,created_at,duration,video_id,vod_offset,game_id,language,thumbnail_url,language_source)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(clip_id) DO UPDATE SET view_count=excluded.view_count,title=excluded.title,
 video_id=excluded.video_id,vod_offset=excluded.vod_offset,game_id=excluded.game_id,language=excluded.language,language_source=excluded.language_source,thumbnail_url=excluded.thumbnail_url,observed_at=now()`, c.ID, c.ChannelID, c.BroadcasterID, c.BroadcasterName, c.Title, c.ViewCount, c.CreatedAt, c.Duration, c.VideoID, c.VODOffset, c.GameID, c.Language, c.ThumbnailURL, c.LanguageSource)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

const clipColumns = `c.clip_id,c.channel_id,c.broadcaster_id,c.broadcaster_name,c.title,c.view_count,c.created_at,c.duration,c.video_id,c.vod_offset,c.state,c.last_error,c.import_id,c.sha256,c.game_id,c.language,c.thumbnail_url,c.language_source`

func scanClip(row pgx.Row) (c domain.Clip, err error) {
	err = row.Scan(&c.ID, &c.ChannelID, &c.BroadcasterID, &c.BroadcasterName, &c.Title, &c.ViewCount, &c.CreatedAt, &c.Duration, &c.VideoID, &c.VODOffset, &c.State, &c.LastError, &c.ImportID, &c.SHA256, &c.GameID, &c.Language, &c.ThumbnailURL, &c.LanguageSource)
	c.URL = "https://clips.twitch.tv/" + c.ID
	return c, translate(err)
}
func (s *Store) ListClips(ctx context.Context, limit int, state string) ([]domain.Clip, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, "SELECT "+clipColumns+" FROM twitch_collector.clips c WHERE ($2='' OR c.state=$2) ORDER BY c.created_at DESC,c.clip_id LIMIT $1", limit, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Clip{}
	for rows.Next() {
		c, e := scanClip(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Reserve(ctx context.Context, clips []domain.Clip, settings domain.Settings, now time.Time) (added int, err error) {
	operation := "begin download reservation"
	defer func() {
		if err != nil {
			err = &domain.StorageError{Operation: operation, Err: err}
		}
	}()
	scfg := settings
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	// Serializes calendar-day quotas across checks and collector replicas.
	operation = "lock daily download quotas"
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(846923102)"); err != nil {
		return 0, err
	}
	operation = "resolve daily download timezone"
	loc, err := time.LoadLocation(scfg.Timezone)
	if err != nil {
		return 0, err
	}
	local := now.In(loc)
	since := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	var total int
	operation = "count today's download reservations"
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM twitch_collector.clips c WHERE reserved_at >= $1
 AND(error_kind<>'capacity' OR EXISTS(SELECT 1 FROM twitch_collector.download_tasks t WHERE t.clip_id=c.clip_id AND COALESCE(t.reservation->>'import_id','')<>''))`, since).Scan(&total); err != nil {
		return 0, err
	}
	for _, c := range clips {
		if total >= scfg.DailyDownloads {
			break
		}
		if c.FlowID != "" {
			var eligible bool
			if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_collector.consumers co JOIN twitch_collector.flows f ON f.id=co.flow_id WHERE co.clip_id=$1 AND co.flow_id=$2 AND co.eligible AND NOT co.excluded AND f.enabled)`, c.ID, c.FlowID).Scan(&eligible); err != nil {
				return 0, err
			}
			if !eligible {
				continue
			}
		}
		var count int
		var active bool
		operation = "check channel availability"
		if err = tx.QueryRow(ctx, "SELECT NOT paused AND NOT archived FROM twitch_collector.channels WHERE id=$1 FOR UPDATE", c.ChannelID).Scan(&active); errors.Is(err, pgx.ErrNoRows) {
			continue
		} else if err != nil {
			return 0, err
		}
		if !active {
			continue
		}
		operation = "count today's channel reservations"
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM twitch_collector.clips c WHERE channel_id=$1 AND reserved_at >= $2
 AND(error_kind<>'capacity' OR EXISTS(SELECT 1 FROM twitch_collector.download_tasks t WHERE t.clip_id=c.clip_id AND COALESCE(t.reservation->>'import_id','')<>''))`, c.ChannelID, since).Scan(&count); err != nil {
			return 0, err
		}
		if count >= scfg.PerChannelDailyDownloads {
			continue
		}
		var overlap bool
		if c.VideoID != "" && c.VODOffset != nil {
			operation = "check VOD interval overlap"
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_collector.clips WHERE video_id=$1 AND vod_offset IS NOT NULL AND duration>0
 AND reserved_at IS NOT NULL AND clip_id<>$4 AND LEAST(vod_offset+duration,$2::double precision+$3::double precision)-GREATEST(vod_offset,$2::double precision)>0)`, c.VideoID, *c.VODOffset, c.Duration, c.ID).Scan(&overlap)
			if err != nil {
				return 0, err
			}
			if overlap {
				continue
			}
		}
		operation = "queue discovered clip"
		tag, e := tx.Exec(ctx, `UPDATE twitch_collector.clips SET state='queued',reserved_at=$2,last_error='' WHERE clip_id=$1 AND state='discovered' AND reserved_at IS NULL`, c.ID, now)
		if e != nil {
			return 0, e
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		operation = "create download task"
		if _, e = tx.Exec(ctx, "INSERT INTO twitch_collector.download_tasks(clip_id,run_at,flow_id,flow_version,epoch) VALUES($1,$2,$3,$4,(SELECT epoch FROM twitch_collector.state WHERE id=1)) ON CONFLICT DO NOTHING", c.ID, now, c.FlowID, c.FlowVersion); e != nil {
			return 0, e
		}
		total++
		added++
	}
	operation = "commit download reservations"
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return added, nil
}

func (s *Store) Claim(ctx context.Context, lease time.Duration) (*domain.Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `WITH exhausted AS (UPDATE twitch_collector.download_tasks SET state='failed',lease_token=NULL,lease_expires_at=NULL,
 last_error='retry budget exhausted after worker interruption',updated_at=now() WHERE state='leased' AND lease_expires_at<=now() AND attempts>=3 RETURNING clip_id)
 UPDATE twitch_collector.clips SET state='failed',last_error='retry budget exhausted after worker interruption' WHERE clip_id IN(SELECT clip_id FROM exhausted)`); err != nil {
		return nil, err
	}
	token := newID()
	var id string
	var attempt int
	var flowID string
	var flowVersion int
	var epoch int64
	var reservation []byte
	err = tx.QueryRow(ctx, `WITH candidate AS (
 SELECT t.clip_id FROM twitch_collector.download_tasks t JOIN twitch_collector.clips c ON c.clip_id=t.clip_id
 JOIN twitch_collector.channels ch ON ch.id=c.channel_id WHERE NOT ch.paused AND NOT ch.archived AND t.attempts<3
 AND (t.flow_id='' OR EXISTS(SELECT 1 FROM twitch_collector.consumers co JOIN twitch_collector.flows f ON f.id=co.flow_id WHERE co.clip_id=t.clip_id AND co.eligible AND NOT co.excluded AND f.enabled))
 AND ((t.state='queued' AND t.run_at<=now()) OR (t.state='leased' AND t.lease_expires_at<=now()))
 ORDER BY t.run_at,t.clip_id FOR UPDATE OF t SKIP LOCKED LIMIT 1)
 UPDATE twitch_collector.download_tasks t SET state='leased',attempts=attempts+1,lease_token=$1,
 lease_expires_at=now()+$2*interval '1 second',updated_at=now(),started_at=now() FROM candidate WHERE t.clip_id=candidate.clip_id RETURNING t.clip_id,t.attempts,t.flow_id,t.flow_version,t.reservation,t.epoch`, token, lease.Seconds()).Scan(&id, &attempt, &flowID, &flowVersion, &reservation, &epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, "UPDATE twitch_collector.clips SET state='downloading' WHERE clip_id=$1", id); err != nil {
		return nil, err
	}
	c, err := scanClip(tx.QueryRow(ctx, "SELECT "+clipColumns+" FROM twitch_collector.clips c WHERE c.clip_id=$1", id))
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	task := &domain.Task{Epoch: epoch, Clip: c, Token: token, Attempt: attempt, FlowID: flowID, FlowVersion: flowVersion}
	if len(reservation) > 0 {
		if err = json.Unmarshal(reservation, &task.Reservation); err != nil {
			return nil, err
		}
	}
	return task, nil
}

const validLease = `t.clip_id=$1 AND t.state='leased' AND t.lease_token=$2 AND t.lease_expires_at>now()`

func (s *Store) ValidLease(ctx context.Context, t domain.Task) error {
	var valid bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM twitch_collector.download_tasks t JOIN twitch_collector.clips c ON c.clip_id=t.clip_id JOIN twitch_collector.channels ch ON ch.id=c.channel_id WHERE `+validLease+` AND NOT ch.paused AND NOT ch.archived)`, t.Clip.ID, t.Token).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return domain.ErrLeaseLost
	}
	return nil
}
func (s *Store) Renew(ctx context.Context, t domain.Task, lease time.Duration) error {
	tag, err := s.pool.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET lease_expires_at=now()+$3*interval '1 second',updated_at=now() FROM twitch_collector.clips c JOIN twitch_collector.channels ch ON ch.id=c.channel_id WHERE c.clip_id=t.clip_id AND `+validLease+` AND NOT ch.paused AND NOT ch.archived`, t.Clip.ID, t.Token, lease.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	return nil
}
func (s *Store) Complete(ctx context.Context, t domain.Task, importID, sha string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET state='done',lease_token=NULL,lease_expires_at=NULL,updated_at=now() FROM twitch_collector.clips c JOIN twitch_collector.channels ch ON ch.id=c.channel_id WHERE c.clip_id=t.clip_id AND `+validLease+` AND NOT ch.paused AND NOT ch.archived`, t.Clip.ID, t.Token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	if _, err = tx.Exec(ctx, "UPDATE twitch_collector.clips SET state='imported',import_id=$2,sha256=$3,last_error='' WHERE clip_id=$1", t.Clip.ID, importID, sha); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Fail(ctx context.Context, t domain.Task, kind, message string, retry *time.Time) error {
	state := "failed"
	taskState := "failed"
	if retry != nil {
		state = "queued"
		taskState = "queued"
	}
	if kind == "duplicate" {
		state = "duplicate"
	}
	var reservation []byte
	var err error
	if t.Reservation.ImportID != "" {
		reservation, err = json.Marshal(t.Reservation)
		if err != nil {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET state=$3,run_at=COALESCE($4,run_at),last_error=$5,reservation=COALESCE($6,reservation),failure_synced_at=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE `+validLease, t.Clip.ID, t.Token, taskState, retry, message, reservation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	if _, err = tx.Exec(ctx, "UPDATE twitch_collector.clips SET state=$2,last_error=$3,error_kind=$4 WHERE clip_id=$1", t.Clip.ID, state, message, kind); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Defer(ctx context.Context, t domain.Task, when time.Time) error {
	return s.DeferWithReason(ctx, t, when, "")
}
func (s *Store) DeferWithReason(ctx context.Context, t domain.Task, when time.Time, message string) error {
	return s.deferTask(ctx, t, when, message, "quota")
}

func (s *Store) DeferForCapacity(ctx context.Context, t domain.Task, when time.Time, message string) error {
	return s.deferTask(ctx, t, when, message, "capacity")
}

func (s *Store) deferTask(ctx context.Context, t domain.Task, when time.Time, message, kind string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE twitch_collector.download_tasks t SET state='queued',attempts=GREATEST(attempts-1,0),run_at=$3,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE `+validLease, t.Clip.ID, t.Token, when)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLeaseLost
	}
	if _, err = tx.Exec(ctx, "UPDATE twitch_collector.clips SET state='queued',last_error=$2,error_kind=CASE WHEN $2='' THEN '' ELSE $3 END WHERE clip_id=$1", t.Clip.ID, message, kind); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) HasSHA256(ctx context.Context, sha, id string) (bool, error) {
	var found bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM twitch_collector.clips WHERE sha256=$1 AND clip_id<>$2 AND state='imported')", sha, id).Scan(&found)
	return found, err
}
func (s *Store) SetLastCheck(ctx context.Context, at time.Time, message string) error {
	_, err := s.pool.Exec(ctx, "UPDATE twitch_collector.state SET last_check_at=CASE WHEN $2='' THEN $1 ELSE last_check_at END,last_error=$2 WHERE id=1", at, message)
	return err
}
func (s *Store) Status(ctx context.Context) (domain.Status, error) {
	var out domain.Status
	err := s.pool.QueryRow(ctx, `SELECT st.last_check_at,st.last_error,(SELECT count(*) FROM twitch_collector.channels WHERE NOT archived),
 (SELECT count(*) FROM twitch_collector.clips WHERE state='queued'),(SELECT count(*) FROM twitch_collector.clips WHERE state IN('downloading','importing','verifying')),
 (SELECT count(*) FROM twitch_collector.clips WHERE state='imported'),(SELECT count(*) FROM twitch_collector.clips WHERE state='failed') FROM twitch_collector.state st WHERE id=1`).Scan(&out.LastCheckAt, &out.LastError, &out.Channels, &out.Queued, &out.Downloading, &out.Imported, &out.Failed)
	return out, err
}
