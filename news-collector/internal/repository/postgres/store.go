// Package postgres keeps all news collector state in the news_collector schema.
package postgres

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/metrics"
	"terex/proto/pgxutil"
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
func (s *Store) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := s.pool.Exec(ctx, sql, args...)
	return err
}

const migration = `
CREATE SCHEMA IF NOT EXISTS news_collector;
CREATE TABLE IF NOT EXISTS news_collector.sources(
 id bigserial PRIMARY KEY, name text NOT NULL, kind text NOT NULL CHECK (kind IN ('rss','gnews')),
 url text NOT NULL DEFAULT '', query text NOT NULL DEFAULT '', lang text NOT NULL DEFAULT 'ru',
 enabled boolean NOT NULL DEFAULT true, fetch_interval_min integer NOT NULL DEFAULT 30,
 last_fetched_at timestamptz, last_error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (kind, url, query, lang));
CREATE TABLE IF NOT EXISTS news_collector.channels(
 id bigserial PRIMARY KEY, title text NOT NULL, chat_id text NOT NULL, bot_token_enc bytea,
 bot_username text NOT NULL DEFAULT '', chat_title text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'unverified', last_error text NOT NULL DEFAULT '', verified_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS news_collector.flows(
 id bigserial PRIMARY KEY, name text NOT NULL, enabled boolean NOT NULL DEFAULT false,
 topic text NOT NULL DEFAULT '', language text NOT NULL DEFAULT 'ru' CHECK (language IN ('ru','en')),
 channel_id bigint REFERENCES news_collector.channels(id) ON DELETE SET NULL,
 approve_mode text NOT NULL DEFAULT 'by_approve' CHECK (approve_mode IN ('by_approve','auto')),
 posts_per_day integer NOT NULL DEFAULT 3, window_start_hour integer NOT NULL DEFAULT 9, window_end_hour integer NOT NULL DEFAULT 22,
 timezone text NOT NULL DEFAULT 'Europe/Moscow', min_interval_min integer NOT NULL DEFAULT 60,
 min_sources integer NOT NULL DEFAULT 1, min_relevance real NOT NULL DEFAULT 0.25, window_hours integer NOT NULL DEFAULT 24,
 max_pending integer NOT NULL DEFAULT 5, style_prompt text NOT NULL DEFAULT '', include_link boolean NOT NULL DEFAULT true,
 max_chars integer NOT NULL DEFAULT 900, model text NOT NULL DEFAULT '', topic_embedding bytea,
 last_generated_at timestamptz, last_error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS news_collector.flow_sources(
 flow_id bigint NOT NULL REFERENCES news_collector.flows(id) ON DELETE CASCADE,
 source_id bigint NOT NULL REFERENCES news_collector.sources(id) ON DELETE CASCADE,
 PRIMARY KEY(flow_id, source_id));
CREATE TABLE IF NOT EXISTS news_collector.articles(
 id bigserial PRIMARY KEY, source_id bigint NOT NULL REFERENCES news_collector.sources(id) ON DELETE CASCADE,
 url text NOT NULL, url_hash text NOT NULL UNIQUE, title text NOT NULL, summary text NOT NULL DEFAULT '',
 text text NOT NULL DEFAULT '', publisher text NOT NULL DEFAULT '', lang text NOT NULL DEFAULT '',
 published_at timestamptz NOT NULL, fetched_at timestamptz NOT NULL DEFAULT now(),
 status text NOT NULL DEFAULT 'new', simhash bigint, embedding bytea, dup_of bigint);
CREATE INDEX IF NOT EXISTS news_articles_status ON news_collector.articles(status, id);
CREATE INDEX IF NOT EXISTS news_articles_published ON news_collector.articles(published_at);
CREATE INDEX IF NOT EXISTS news_articles_source ON news_collector.articles(source_id);
CREATE TABLE IF NOT EXISTS news_collector.clusters(
 id bigserial PRIMARY KEY, flow_id bigint NOT NULL REFERENCES news_collector.flows(id) ON DELETE CASCADE,
 title text NOT NULL, status text NOT NULL DEFAULT 'open', centroid bytea NOT NULL, relevance real NOT NULL DEFAULT 0,
 first_at timestamptz NOT NULL, last_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS news_clusters_flow ON news_collector.clusters(flow_id, status, last_at DESC);
CREATE TABLE IF NOT EXISTS news_collector.flow_articles(
 flow_id bigint NOT NULL REFERENCES news_collector.flows(id) ON DELETE CASCADE,
 article_id bigint NOT NULL REFERENCES news_collector.articles(id) ON DELETE CASCADE,
 cluster_id bigint REFERENCES news_collector.clusters(id) ON DELETE CASCADE, relevance real NOT NULL DEFAULT 0,
 PRIMARY KEY(flow_id, article_id));
CREATE INDEX IF NOT EXISTS news_flow_articles_cluster ON news_collector.flow_articles(cluster_id);
CREATE TABLE IF NOT EXISTS news_collector.posts(
 id bigserial PRIMARY KEY, flow_id bigint NOT NULL REFERENCES news_collector.flows(id) ON DELETE CASCADE,
 cluster_id bigint REFERENCES news_collector.clusters(id) ON DELETE SET NULL,
 title text NOT NULL DEFAULT '', text text NOT NULL, links jsonb NOT NULL DEFAULT '[]',
 status text NOT NULL DEFAULT 'pending', scheduled_at timestamptz, published_at timestamptz, message_id bigint,
 error text NOT NULL DEFAULT '', attempts integer NOT NULL DEFAULT 0, score real NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS news_posts_status ON news_collector.posts(status, scheduled_at);
CREATE INDEX IF NOT EXISTS news_posts_flow ON news_collector.posts(flow_id, created_at DESC);
ALTER TABLE news_collector.articles ADD COLUMN IF NOT EXISTS image_url text NOT NULL DEFAULT '';
ALTER TABLE news_collector.flows
 ADD COLUMN IF NOT EXISTS image_mode text NOT NULL DEFAULT 'auto' CHECK (image_mode IN ('none','article','web','auto')),
 ADD COLUMN IF NOT EXISTS use_emoji boolean NOT NULL DEFAULT true,
 ADD COLUMN IF NOT EXISTS use_hashtags boolean NOT NULL DEFAULT true,
 ADD COLUMN IF NOT EXISTS footer text NOT NULL DEFAULT '';
ALTER TABLE news_collector.posts
 ADD COLUMN IF NOT EXISTS emoji text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS hashtags text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS image_url text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS image_source text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS image_credit text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS image_credit_url text NOT NULL DEFAULT '',
 ADD COLUMN IF NOT EXISTS image_query text NOT NULL DEFAULT '';
`

func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(846923177)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, migration); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---- helpers ----

func EncodeVec(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	b := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func DecodeVec(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return fmt.Errorf("%w: already exists", domain.ErrConflict)
		case "23503":
			return fmt.Errorf("%w: referenced record does not exist", domain.ErrInvalid)
		case "23514":
			return fmt.Errorf("%w: value is out of range", domain.ErrInvalid)
		}
	}
	return err
}

// ---- sources ----

const sourceCols = `s.id, s.name, s.kind, s.url, s.query, s.lang, s.enabled, s.fetch_interval_min, s.last_fetched_at, s.last_error, s.created_at,
 (SELECT count(*) FROM news_collector.articles a WHERE a.source_id=s.id AND a.published_at > now() - interval '7 days')`

func scanSource(r pgx.Row) (domain.Source, error) {
	var x domain.Source
	err := r.Scan(&x.ID, &x.Name, &x.Kind, &x.URL, &x.Query, &x.Lang, &x.Enabled, &x.FetchIntervalMin, &x.LastFetchedAt, &x.LastError, &x.CreatedAt, &x.Articles)
	return x, err
}

func (s *Store) Sources(ctx context.Context) ([]domain.Source, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sourceCols+` FROM news_collector.sources s ORDER BY s.name, s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Source{}
	for rows.Next() {
		x, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) Source(ctx context.Context, id int64) (domain.Source, error) {
	x, err := scanSource(s.pool.QueryRow(ctx, `SELECT `+sourceCols+` FROM news_collector.sources s WHERE s.id=$1`, id))
	return x, mapErr(err)
}

func (s *Store) CreateSource(ctx context.Context, x domain.Source) (domain.Source, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO news_collector.sources(name,kind,url,query,lang,enabled,fetch_interval_min) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		x.Name, x.Kind, x.URL, x.Query, x.Lang, x.Enabled, x.FetchIntervalMin).Scan(&id)
	if err != nil {
		return x, mapErr(err)
	}
	return s.Source(ctx, id)
}

func (s *Store) UpdateSource(ctx context.Context, x domain.Source) (domain.Source, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE news_collector.sources SET name=$2, url=$3, query=$4, lang=$5, enabled=$6, fetch_interval_min=$7 WHERE id=$1`,
		x.ID, x.Name, x.URL, x.Query, x.Lang, x.Enabled, x.FetchIntervalMin)
	if err != nil {
		return x, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return x, domain.ErrNotFound
	}
	return s.Source(ctx, x.ID)
}

func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM news_collector.sources WHERE id=$1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// DueSources are enabled sources whose feed is older than their interval. Sources that
// no enabled flow uses are skipped; "fetch now" bypasses this.
func (s *Store) DueSources(ctx context.Context) ([]domain.Source, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+sourceCols+` FROM news_collector.sources s
 WHERE s.enabled AND (s.last_fetched_at IS NULL OR s.last_fetched_at < now() - make_interval(mins => s.fetch_interval_min))
   AND EXISTS (SELECT 1 FROM news_collector.flow_sources fs JOIN news_collector.flows f ON f.id=fs.flow_id WHERE fs.source_id=s.id AND f.enabled)
 ORDER BY s.last_fetched_at NULLS FIRST LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Source
	for rows.Next() {
		x, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) MarkFetched(ctx context.Context, id int64, fetchErr string) error {
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.sources SET last_fetched_at=now(), last_error=$2 WHERE id=$1`, id, fetchErr)
	return err
}

// ---- articles ----

// InsertArticle stores a new article and reports whether it was new. An article that is already
// known only gets its picture filled in when it had none (so articles stored before pictures were
// collected are repaired on the next feed pass); that is not counted as new.
func (s *Store) InsertArticle(ctx context.Context, a domain.Article, urlHash string) (bool, error) {
	var inserted bool
	err := s.pool.QueryRow(ctx, `INSERT INTO news_collector.articles(source_id,url,url_hash,title,summary,publisher,lang,published_at,image_url)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)
 ON CONFLICT (url_hash) DO UPDATE SET image_url = EXCLUDED.image_url
   WHERE news_collector.articles.image_url = '' AND EXCLUDED.image_url <> ''
 RETURNING (xmax = 0)`,
		a.SourceID, a.URL, urlHash, a.Title, a.Summary, a.Publisher, a.Lang, a.PublishedAt, a.ImageURL).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // known article, nothing to repair
	}
	return inserted, err
}

const articleCols = `a.id, a.source_id, a.url, a.title, a.summary, a.text, a.publisher, a.lang, a.published_at, a.status, a.simhash, a.embedding, a.dup_of, a.image_url`

func scanArticle(r pgx.Row) (domain.Article, error) {
	var a domain.Article
	var sim *int64
	var emb []byte
	err := r.Scan(&a.ID, &a.SourceID, &a.URL, &a.Title, &a.Summary, &a.Text, &a.Publisher, &a.Lang, &a.PublishedAt, &a.Status, &sim, &emb, &a.DupOf, &a.ImageURL)
	if sim != nil {
		a.SimHash = uint64(*sim)
	}
	a.Embedding = DecodeVec(emb)
	return a, err
}

func (s *Store) articles(ctx context.Context, q string, args ...any) ([]domain.Article, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Article
	for rows.Next() {
		a, err := scanArticle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ArticlesByStatus(ctx context.Context, status string, limit int) ([]domain.Article, error) {
	return s.articles(ctx, `SELECT `+articleCols+` FROM news_collector.articles a WHERE a.status=$1 ORDER BY a.id LIMIT $2`, status, limit)
}

// SetExtracted stores the page text; a picture found on the page fills in only when the feed had none.
func (s *Store) SetExtracted(ctx context.Context, id int64, text, lang string, sim uint64, image string) error {
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.articles SET text=$2, lang=$3, simhash=$4, status='extracted',
 image_url = COALESCE(NULLIF(image_url, ''), $5) WHERE id=$1`, id, text, lang, int64(sim), image)
	return err
}

func (s *Store) MarkDuplicate(ctx context.Context, id, of int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.articles SET status='duplicate', dup_of=$2 WHERE id=$1`, id, of)
	return err
}

func (s *Store) SetEmbedding(ctx context.Context, id int64, v []float32) error {
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.articles SET embedding=$2, status='ready' WHERE id=$1`, id, EncodeVec(v))
	return err
}

type Hash struct {
	ID       int64
	SourceID int64
	SimHash  uint64
}

// RecentHashes returns simhashes of non-duplicate articles for near-duplicate detection.
func (s *Store) RecentHashes(ctx context.Context, since time.Time) ([]Hash, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, source_id, simhash FROM news_collector.articles WHERE published_at >= $1 AND simhash IS NOT NULL AND status IN ('extracted','ready')`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hash
	for rows.Next() {
		var h Hash
		var v int64
		if err = rows.Scan(&h.ID, &h.SourceID, &v); err != nil {
			return nil, err
		}
		h.SimHash = uint64(v)
		out = append(out, h)
	}
	return out, rows.Err()
}

type Vec struct {
	ID  int64
	Vec []float32
}

// RecentEmbeddings returns embeddings of ready articles for semantic duplicate detection.
func (s *Store) RecentEmbeddings(ctx context.Context, since time.Time) ([]Vec, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, embedding FROM news_collector.articles WHERE published_at >= $1 AND status='ready' AND embedding IS NOT NULL`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Vec
	for rows.Next() {
		var v Vec
		var b []byte
		if err = rows.Scan(&v.ID, &b); err != nil {
			return nil, err
		}
		v.Vec = DecodeVec(b)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RecentArticles(ctx context.Context, flowID int64, limit int) ([]domain.Article, error) {
	q := `SELECT ` + articleCols + `, src.name FROM news_collector.articles a JOIN news_collector.sources src ON src.id=a.source_id`
	args := []any{}
	if flowID > 0 {
		q += ` WHERE a.source_id IN (SELECT source_id FROM news_collector.flow_sources WHERE flow_id=$1)`
		args = append(args, flowID)
	}
	q += fmt.Sprintf(` ORDER BY a.published_at DESC LIMIT %d`, limit)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Article{}
	for rows.Next() {
		var a domain.Article
		var sim *int64
		var emb []byte
		if err = rows.Scan(&a.ID, &a.SourceID, &a.URL, &a.Title, &a.Summary, &a.Text, &a.Publisher, &a.Lang, &a.PublishedAt, &a.Status, &sim, &emb, &a.DupOf, &a.ImageURL, &a.SourceName); err != nil {
			return nil, err
		}
		a.Text = ""
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteOldArticles(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM news_collector.articles WHERE published_at < $1`, before)
	return tag.RowsAffected(), err
}

// ---- channels ----

const channelCols = `id, title, chat_id, bot_token_enc, bot_username, chat_title, status, last_error, verified_at, created_at`

func scanChannel(r pgx.Row) (domain.Channel, error) {
	var c domain.Channel
	err := r.Scan(&c.ID, &c.Title, &c.ChatID, &c.TokenEnc, &c.BotUsername, &c.ChatTitle, &c.Status, &c.LastError, &c.VerifiedAt, &c.CreatedAt)
	c.HasToken = len(c.TokenEnc) > 0
	return c, err
}

func (s *Store) Channels(ctx context.Context) ([]domain.Channel, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+channelCols+` FROM news_collector.channels ORDER BY title, id`)
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

func (s *Store) Channel(ctx context.Context, id int64) (domain.Channel, error) {
	c, err := scanChannel(s.pool.QueryRow(ctx, `SELECT `+channelCols+` FROM news_collector.channels WHERE id=$1`, id))
	return c, mapErr(err)
}

func (s *Store) CreateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO news_collector.channels(title,chat_id,bot_token_enc) VALUES($1,$2,$3) RETURNING id`, c.Title, c.ChatID, c.TokenEnc).Scan(&id)
	if err != nil {
		return c, mapErr(err)
	}
	return s.Channel(ctx, id)
}

// UpdateChannel changes title/chat; a non-nil token replaces the stored one. Any change to
// the destination resets verification.
func (s *Store) UpdateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE news_collector.channels SET title=$2,
 status = CASE WHEN chat_id <> $3 OR $4::bytea IS NOT NULL THEN 'unverified' ELSE status END,
 verified_at = CASE WHEN chat_id <> $3 OR $4::bytea IS NOT NULL THEN NULL ELSE verified_at END,
 chat_id=$3, bot_token_enc=COALESCE($4, bot_token_enc) WHERE id=$1`, c.ID, c.Title, c.ChatID, c.TokenEnc)
	if err != nil {
		return c, mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return c, domain.ErrNotFound
	}
	return s.Channel(ctx, c.ID)
}

func (s *Store) SetChannelVerification(ctx context.Context, id int64, status, botUsername, chatTitle, lastErr string) error {
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.channels SET status=$2, bot_username=$3, chat_title=$4, last_error=$5,
 verified_at = CASE WHEN $2='ok' THEN now() ELSE verified_at END WHERE id=$1`, id, status, botUsername, chatTitle, lastErr)
	return err
}

func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM news_collector.channels WHERE id=$1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ---- flows ----

const flowCols = `f.id, f.name, f.enabled, f.topic, f.language, f.channel_id, f.approve_mode, f.posts_per_day, f.window_start_hour, f.window_end_hour,
 f.timezone, f.min_interval_min, f.min_sources, f.min_relevance, f.window_hours, f.max_pending, f.style_prompt, f.include_link, f.max_chars, f.model, f.image_mode, f.use_emoji, f.use_hashtags, f.footer,
 COALESCE((SELECT array_agg(fs.source_id ORDER BY fs.source_id) FROM news_collector.flow_sources fs WHERE fs.flow_id=f.id), '{}'),
 f.last_generated_at, f.last_error, f.created_at, f.updated_at, f.topic_embedding,
 COALESCE(c.title,''),
 (SELECT count(*) FROM news_collector.posts p WHERE p.flow_id=f.id AND p.status='pending'),
 (SELECT count(*) FROM news_collector.posts p WHERE p.flow_id=f.id AND p.status='published' AND p.published_at >= now() - interval '24 hours'),
 (SELECT count(*) FROM news_collector.clusters k WHERE k.flow_id=f.id AND k.status='open' AND k.last_at >= now() - make_interval(hours => f.window_hours))`

const flowFrom = ` FROM news_collector.flows f LEFT JOIN news_collector.channels c ON c.id=f.channel_id`

func scanFlow(r pgx.Row) (domain.Flow, error) {
	var f domain.Flow
	var emb []byte
	err := r.Scan(&f.ID, &f.Name, &f.Enabled, &f.Topic, &f.Language, &f.ChannelID, &f.ApproveMode, &f.PostsPerDay, &f.WindowStart, &f.WindowEnd,
		&f.Timezone, &f.MinIntervalMin, &f.MinSources, &f.MinRelevance, &f.WindowHours, &f.MaxPending, &f.StylePrompt, &f.IncludeLink, &f.MaxChars, &f.Model, &f.ImageMode, &f.UseEmoji, &f.UseHashtags, &f.Footer,
		&f.SourceIDs, &f.LastGenerated, &f.LastError, &f.CreatedAt, &f.UpdatedAt, &emb, &f.ChannelTitle, &f.Pending, &f.PostedToday, &f.OpenClusters)
	f.TopicEmbedding = DecodeVec(emb)
	if f.SourceIDs == nil {
		f.SourceIDs = []int64{}
	}
	return f, err
}

func (s *Store) Flows(ctx context.Context) ([]domain.Flow, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+flowCols+flowFrom+` ORDER BY f.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Flow{}
	for rows.Next() {
		f, err := scanFlow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) Flow(ctx context.Context, id int64) (domain.Flow, error) {
	f, err := scanFlow(s.pool.QueryRow(ctx, `SELECT `+flowCols+flowFrom+` WHERE f.id=$1`, id))
	return f, mapErr(err)
}

func (s *Store) SaveFlow(ctx context.Context, f domain.Flow) (domain.Flow, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return f, err
	}
	defer tx.Rollback(ctx)
	if f.ID == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO news_collector.flows(name,enabled,topic,language,channel_id,approve_mode,posts_per_day,window_start_hour,window_end_hour,timezone,
 min_interval_min,min_sources,min_relevance,window_hours,max_pending,style_prompt,include_link,max_chars,model,image_mode,use_emoji,use_hashtags,footer)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23) RETURNING id`,
			f.Name, f.Enabled, f.Topic, f.Language, f.ChannelID, f.ApproveMode, f.PostsPerDay, f.WindowStart, f.WindowEnd, f.Timezone,
			f.MinIntervalMin, f.MinSources, f.MinRelevance, f.WindowHours, f.MaxPending, f.StylePrompt, f.IncludeLink, f.MaxChars, f.Model,
			f.ImageMode, f.UseEmoji, f.UseHashtags, f.Footer).Scan(&f.ID)
	} else {
		// Articles rejected as off-topic were judged against the old topic and threshold. Forget those
		// verdicts when either changes, so the next pass evaluates them again instead of ignoring them forever.
		if _, err = tx.Exec(ctx, `DELETE FROM news_collector.flow_articles fa USING news_collector.flows f
 WHERE f.id=$1 AND fa.flow_id=f.id AND fa.cluster_id IS NULL AND (f.topic <> $2 OR f.min_relevance <> $3::real)`, f.ID, f.Topic, f.MinRelevance); err != nil {
			return f, err
		}
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `UPDATE news_collector.flows SET name=$2,enabled=$3,topic=$4,language=$5,channel_id=$6,approve_mode=$7,posts_per_day=$8,window_start_hour=$9,
 window_end_hour=$10,timezone=$11,min_interval_min=$12,min_sources=$13,min_relevance=$14,window_hours=$15,max_pending=$16,style_prompt=$17,include_link=$18,
 max_chars=$19,model=$20,image_mode=$21,use_emoji=$22,use_hashtags=$23,footer=$24, updated_at=now(),
 topic_embedding = CASE WHEN topic <> $4 THEN NULL ELSE topic_embedding END WHERE id=$1`,
			f.ID, f.Name, f.Enabled, f.Topic, f.Language, f.ChannelID, f.ApproveMode, f.PostsPerDay, f.WindowStart, f.WindowEnd, f.Timezone,
			f.MinIntervalMin, f.MinSources, f.MinRelevance, f.WindowHours, f.MaxPending, f.StylePrompt, f.IncludeLink, f.MaxChars, f.Model,
			f.ImageMode, f.UseEmoji, f.UseHashtags, f.Footer)
		if err == nil && tag.RowsAffected() == 0 {
			return f, domain.ErrNotFound
		}
	}
	if err != nil {
		return f, mapErr(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM news_collector.flow_sources WHERE flow_id=$1`, f.ID); err != nil {
		return f, err
	}
	for _, sid := range f.SourceIDs {
		if _, err = tx.Exec(ctx, `INSERT INTO news_collector.flow_sources(flow_id,source_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, f.ID, sid); err != nil {
			return f, mapErr(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return f, err
	}
	return s.Flow(ctx, f.ID)
}

func (s *Store) DeleteFlow(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM news_collector.flows WHERE id=$1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) SetTopicEmbedding(ctx context.Context, id int64, topic string, v []float32) error {
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.flows SET topic_embedding=$3 WHERE id=$1 AND topic=$2`, id, topic, EncodeVec(v))
	return err
}

func (s *Store) SetFlowResult(ctx context.Context, id int64, generated bool, lastErr string) error {
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.flows SET last_error=$2, last_generated_at = CASE WHEN $3 THEN now() ELSE last_generated_at END WHERE id=$1`, id, lastErr, generated)
	return err
}

// ---- clustering ----

// UnassignedArticles are ready articles of the flow's sources that the flow has not evaluated yet.
func (s *Store) UnassignedArticles(ctx context.Context, flowID int64, since time.Time, limit int) ([]domain.Article, error) {
	return s.articles(ctx, `SELECT `+articleCols+` FROM news_collector.articles a
 WHERE a.status='ready' AND a.published_at >= $2
   AND a.source_id IN (SELECT source_id FROM news_collector.flow_sources WHERE flow_id=$1)
   AND NOT EXISTS (SELECT 1 FROM news_collector.flow_articles fa WHERE fa.flow_id=$1 AND fa.article_id=a.id)
 ORDER BY a.published_at LIMIT $3`, flowID, since, limit)
}

func (s *Store) RecordIrrelevant(ctx context.Context, flowID, articleID int64, relevance float64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO news_collector.flow_articles(flow_id,article_id,relevance) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, flowID, articleID, relevance)
	return err
}

// OpenClusters returns the flow's open clusters touched since the cutoff.
func (s *Store) OpenClusters(ctx context.Context, flowID int64, since time.Time) ([]domain.Cluster, error) {
	return s.clusters(ctx, `WHERE k.flow_id=$1 AND k.status='open' AND k.last_at >= $2`, flowID, since)
}

// RecentUsedClusters returns clusters already turned into posts, for "already covered" checks.
func (s *Store) RecentUsedClusters(ctx context.Context, flowID int64, since time.Time) ([]domain.Cluster, error) {
	return s.clusters(ctx, `WHERE k.flow_id=$1 AND k.status IN ('used') AND k.last_at >= $2`, flowID, since)
}

func (s *Store) clusters(ctx context.Context, where string, args ...any) ([]domain.Cluster, error) {
	rows, err := s.pool.Query(ctx, `SELECT k.id, k.flow_id, k.title, k.status, k.centroid, k.relevance, k.first_at, k.last_at,
 (SELECT count(DISTINCT COALESCE(NULLIF(a.publisher,''), 'src:' || a.source_id::text)) FROM news_collector.flow_articles fa JOIN news_collector.articles a ON a.id=fa.article_id WHERE fa.cluster_id=k.id),
 (SELECT count(*) FROM news_collector.flow_articles fa WHERE fa.cluster_id=k.id)
 FROM news_collector.clusters k `+where+` ORDER BY k.last_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Cluster{}
	for rows.Next() {
		var k domain.Cluster
		var b []byte
		if err = rows.Scan(&k.ID, &k.FlowID, &k.Title, &k.Status, &b, &k.Relevance, &k.FirstAt, &k.LastAt, &k.Sources, &k.Articles); err != nil {
			return nil, err
		}
		k.Centroid = DecodeVec(b)
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) Cluster(ctx context.Context, id int64) (domain.Cluster, error) {
	list, err := s.clusters(ctx, `WHERE k.id=$1`, id)
	if err != nil {
		return domain.Cluster{}, err
	}
	if len(list) == 0 {
		return domain.Cluster{}, domain.ErrNotFound
	}
	return list[0], nil
}

func (s *Store) CreateCluster(ctx context.Context, flowID int64, a domain.Article, relevance float64) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO news_collector.clusters(flow_id,title,centroid,relevance,first_at,last_at) VALUES($1,$2,$3,$4,$5,$5) RETURNING id`,
		flowID, a.Title, EncodeVec(a.Embedding), relevance, a.PublishedAt).Scan(&id); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO news_collector.flow_articles(flow_id,article_id,cluster_id,relevance) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, flowID, a.ID, id, relevance); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// AddToCluster attaches an article and stores the recomputed centroid.
func (s *Store) AddToCluster(ctx context.Context, flowID, clusterID int64, a domain.Article, relevance float64, centroid []float32) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO news_collector.flow_articles(flow_id,article_id,cluster_id,relevance) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, flowID, a.ID, clusterID, relevance); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE news_collector.clusters SET centroid=$2, relevance=GREATEST(relevance,$3), last_at=GREATEST(last_at,$4), first_at=LEAST(first_at,$4) WHERE id=$1`,
		clusterID, EncodeVec(centroid), relevance, a.PublishedAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetClusterStatus(ctx context.Context, id int64, status string) error {
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.clusters SET status=$2 WHERE id=$1`, id, status)
	return err
}

func (s *Store) ClusterArticles(ctx context.Context, clusterID int64, limit int) ([]domain.Article, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+articleCols+`, src.name FROM news_collector.flow_articles fa
 JOIN news_collector.articles a ON a.id=fa.article_id JOIN news_collector.sources src ON src.id=a.source_id
 WHERE fa.cluster_id=$1 ORDER BY length(a.text) DESC, a.published_at LIMIT $2`, clusterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Article
	for rows.Next() {
		var a domain.Article
		var sim *int64
		var emb []byte
		if err = rows.Scan(&a.ID, &a.SourceID, &a.URL, &a.Title, &a.Summary, &a.Text, &a.Publisher, &a.Lang, &a.PublishedAt, &a.Status, &sim, &emb, &a.DupOf, &a.ImageURL, &a.SourceName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- posts ----

const postCols = `p.id, p.flow_id, f.name, p.cluster_id, f.channel_id, p.title, p.text, p.links, p.status, p.scheduled_at, p.published_at, p.message_id, p.error, p.attempts, p.score, p.emoji, p.hashtags, p.image_url, p.image_source, p.image_credit, p.image_credit_url, p.image_query, p.created_at, p.updated_at`
const postFrom = ` FROM news_collector.posts p JOIN news_collector.flows f ON f.id=p.flow_id`

func scanPost(r pgx.Row) (domain.Post, error) {
	var p domain.Post
	err := r.Scan(&p.ID, &p.FlowID, &p.FlowName, &p.ClusterID, &p.ChannelID, &p.Title, &p.Text, &p.Links, &p.Status, &p.ScheduledAt, &p.PublishedAt, &p.MessageID, &p.Error, &p.Attempts, &p.Score, &p.Emoji, &p.Hashtags, &p.ImageURL, &p.ImageSource, &p.ImageCredit, &p.ImageCreditURL, &p.ImageQuery, &p.CreatedAt, &p.UpdatedAt)
	if p.Links == nil {
		p.Links = []domain.Link{}
	}
	return p, err
}

func (s *Store) posts(ctx context.Context, tail string, args ...any) ([]domain.Post, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+postCols+postFrom+` `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Post{}
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Post(ctx context.Context, id int64) (domain.Post, error) {
	p, err := scanPost(s.pool.QueryRow(ctx, `SELECT `+postCols+postFrom+` WHERE p.id=$1`, id))
	return p, mapErr(err)
}

// ListPosts lists posts newest first. With a from/to range it returns the posts whose calendar time
// (published_at, else scheduled_at) falls inside it, oldest first; drafts without a time are excluded.
func (s *Store) ListPosts(ctx context.Context, flowID int64, status string, from, to *time.Time, limit int) ([]domain.Post, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if flowID > 0 {
		add("p.flow_id=$%d", flowID)
	}
	if status != "" {
		add("p.status=$%d", status)
	}
	const when = "COALESCE(p.published_at, p.scheduled_at)"
	if from != nil {
		add(when+" >= $%d", *from)
	}
	if to != nil {
		add(when+" < $%d", *to)
	}
	tail := ""
	if len(where) > 0 {
		tail = "WHERE " + strings.Join(where, " AND ")
	}
	order := "ORDER BY p.created_at DESC, p.id DESC"
	if from != nil || to != nil {
		order = "ORDER BY " + when + ", p.id"
	}
	return s.posts(ctx, tail+" "+order+fmt.Sprintf(" LIMIT %d", limit), args...)
}

// Hold takes a queued post back to review and forgets its time.
func (s *Store) Hold(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE news_collector.posts SET status='pending', scheduled_at=NULL, error='', updated_at=now() WHERE id=$1 AND status='approved'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: only a queued post can be taken back to review", domain.ErrConflict)
	}
	return nil
}

func (s *Store) CreatePost(ctx context.Context, p domain.Post) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO news_collector.posts(flow_id,cluster_id,title,text,links,status,scheduled_at,score,emoji,hashtags,image_url,image_source,image_credit,image_credit_url,image_query)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id`,
		p.FlowID, p.ClusterID, p.Title, p.Text, p.Links, p.Status, p.ScheduledAt, p.Score, p.Emoji, p.Hashtags, p.ImageURL, p.ImageSource, p.ImageCredit, p.ImageCreditURL, p.ImageQuery).Scan(&id)
	return id, err
}

// EditPost changes the content of a post that has not been published yet. The picture and the
// schedule are replaced only when the matching flag is set.
func (s *Store) EditPost(ctx context.Context, id int64, p domain.Post, setSchedule, setImage bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE news_collector.posts SET title=$2, text=$3, emoji=$4, hashtags=$5,
 scheduled_at = CASE WHEN $7 THEN $6 ELSE scheduled_at END,
 image_url = CASE WHEN $8 THEN $9 ELSE image_url END, image_source = CASE WHEN $8 THEN $10 ELSE image_source END,
 image_credit = CASE WHEN $8 THEN $11 ELSE image_credit END, image_credit_url = CASE WHEN $8 THEN $12 ELSE image_credit_url END,
 updated_at=now()
 WHERE id=$1 AND status IN ('pending','approved','failed')`, id, p.Title, p.Text, p.Emoji, p.Hashtags, p.ScheduledAt, setSchedule,
		setImage, p.ImageURL, p.ImageSource, p.ImageCredit, p.ImageCreditURL)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: post is not editable", domain.ErrConflict)
	}
	return nil
}

// Transition moves a post between states atomically; it fails if the current status is not in from.
func (s *Store) Transition(ctx context.Context, id int64, from []string, to string, scheduled *time.Time) error {
	tag, err := s.pool.Exec(ctx, `UPDATE news_collector.posts SET status=$3, scheduled_at=COALESCE($4, scheduled_at), error='', attempts=CASE WHEN $3='approved' THEN 0 ELSE attempts END, updated_at=now()
 WHERE id=$1 AND status = ANY($2)`, id, from, to, scheduled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: post is not in a state that allows this", domain.ErrConflict)
	}
	return nil
}

// DuePosts are approved posts whose time has come, oldest first.
func (s *Store) DuePosts(ctx context.Context, now time.Time, limit int) ([]domain.Post, error) {
	return s.posts(ctx, fmt.Sprintf(`WHERE p.status='approved' AND COALESCE(p.scheduled_at, p.created_at) <= $1 ORDER BY COALESCE(p.scheduled_at, p.created_at) LIMIT %d`, limit), now)
}

// ClaimPost marks a due post as being sent so a second tick cannot send it twice.
func (s *Store) ClaimPost(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE news_collector.posts SET status='sending', updated_at=now() WHERE id=$1 AND status='approved'`, id)
	return tag.RowsAffected() == 1, err
}

func (s *Store) MarkPublished(ctx context.Context, id, messageID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.posts SET status='published', published_at=now(), message_id=$2, error='', updated_at=now() WHERE id=$1`, id, messageID)
	return err
}

// MarkSendFailed returns a post to approved with a later time, or fails it for good.
func (s *Store) MarkSendFailed(ctx context.Context, id int64, errText string, retryAt *time.Time) error {
	if retryAt != nil {
		_, err := s.pool.Exec(ctx, `UPDATE news_collector.posts SET status='approved', scheduled_at=$3, error=$2, attempts=attempts+1, updated_at=now() WHERE id=$1`, id, errText, *retryAt)
		return err
	}
	_, err := s.pool.Exec(ctx, `UPDATE news_collector.posts SET status='failed', error=$2, attempts=attempts+1, updated_at=now() WHERE id=$1`, id, errText)
	return err
}

// RecoverSending returns posts stuck in 'sending' (a crash mid-send) to failed: the message may or
// may not have reached Telegram, so a human decides whether to retry.
func (s *Store) RecoverSending(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE news_collector.posts SET status='failed', error='interrupted while sending; check the channel before retrying', updated_at=now() WHERE status='sending' AND updated_at < $1`, olderThan)
	return tag.RowsAffected(), err
}

func (s *Store) ExpirePending(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE news_collector.posts SET status='expired', updated_at=now() WHERE status='pending' AND created_at < $1`, before)
	return tag.RowsAffected(), err
}

// CountPosts counts a flow's posts created since the cutoff that are not rejected/expired.
func (s *Store) CountPosts(ctx context.Context, flowID int64, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM news_collector.posts WHERE flow_id=$1 AND created_at >= $2 AND status NOT IN ('rejected','expired')`, flowID, since).Scan(&n)
	return n, err
}

func (s *Store) CountPending(ctx context.Context, flowID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM news_collector.posts WHERE flow_id=$1 AND status='pending'`, flowID).Scan(&n)
	return n, err
}

func (s *Store) LastPostCreated(ctx context.Context, flowID int64) (*time.Time, error) {
	var t *time.Time
	err := s.pool.QueryRow(ctx, `SELECT max(created_at) FROM news_collector.posts WHERE flow_id=$1 AND status NOT IN ('rejected','expired')`, flowID).Scan(&t)
	return t, err
}

// ---- status ----

func (s *Store) Counters(ctx context.Context, st *domain.Status) error {
	return s.pool.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM news_collector.flows), (SELECT count(*) FROM news_collector.flows WHERE enabled),
 (SELECT count(*) FROM news_collector.sources), (SELECT count(*) FROM news_collector.channels),
 (SELECT count(*) FROM news_collector.articles WHERE fetched_at >= now() - interval '24 hours'),
 (SELECT count(*) FROM news_collector.articles WHERE status='extracted'),
 (SELECT count(*) FROM news_collector.posts WHERE status='pending'),
 (SELECT max(last_fetched_at) FROM news_collector.sources)`).
		Scan(&st.Flows, &st.EnabledFlows, &st.Sources, &st.Channels, &st.Articles24h, &st.Unembedded, &st.PendingPosts, &st.LastFetchAt)
}

// MetricsSnapshot reads the state gauges exported to Prometheus.
func (s *Store) MetricsSnapshot(ctx context.Context) (metrics.Snapshot, error) {
	snap := metrics.Snapshot{
		PostsByStatus: map[string]int{}, ArticlesByStatus: map[string]int{}, SourcesByState: map[string]int{},
		ChannelsByStatus: map[string]int{}, OpenStories: map[int64]int{}, LastPublishedAt: map[int64]time.Time{},
	}
	counts := func(query string, into map[string]int) error {
		rows, err := s.pool.Query(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if err = rows.Scan(&k, &n); err != nil {
				return err
			}
			into[k] = n
		}
		return rows.Err()
	}
	for query, into := range map[string]map[string]int{
		`SELECT status, count(*) FROM news_collector.posts GROUP BY status`:                                                                                snap.PostsByStatus,
		`SELECT status, count(*) FROM news_collector.articles GROUP BY status`:                                                                             snap.ArticlesByStatus,
		`SELECT status, count(*) FROM news_collector.channels GROUP BY status`:                                                                             snap.ChannelsByStatus,
		`SELECT CASE WHEN NOT enabled THEN 'disabled' WHEN last_error <> '' THEN 'failing' ELSE 'ok' END, count(*) FROM news_collector.sources GROUP BY 1`: snap.SourcesByState,
	} {
		if err := counts(query, into); err != nil {
			return snap, err
		}
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE enabled) FROM news_collector.flows`).Scan(&snap.FlowsTotal, &snap.FlowsEnabled); err != nil {
		return snap, err
	}
	if err := s.pool.QueryRow(ctx, `SELECT
 COALESCE((SELECT EXTRACT(EPOCH FROM now() - min(created_at)) FROM news_collector.posts WHERE status='pending'), 0)::float8,
 COALESCE((SELECT EXTRACT(EPOCH FROM now() - min(fetched_at)) FROM news_collector.articles WHERE status='extracted'), 0)::float8`).
		Scan(&snap.OldestPendingAge, &snap.OldestUnembeddedAge); err != nil {
		return snap, err
	}
	rows, err := s.pool.Query(ctx, `SELECT k.flow_id, count(*) FROM news_collector.clusters k JOIN news_collector.flows f ON f.id=k.flow_id
 WHERE k.status='open' AND k.last_at >= now() - make_interval(hours => f.window_hours) GROUP BY k.flow_id`)
	if err != nil {
		return snap, err
	}
	for rows.Next() {
		var id int64
		var n int
		if err = rows.Scan(&id, &n); err != nil {
			rows.Close()
			return snap, err
		}
		snap.OpenStories[id] = n
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return snap, err
	}
	rows, err = s.pool.Query(ctx, `SELECT flow_id, max(published_at) FROM news_collector.posts WHERE status='published' GROUP BY flow_id`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var t *time.Time
		if err = rows.Scan(&id, &t); err != nil {
			return snap, err
		}
		if t != nil {
			snap.LastPublishedAt[id] = *t
		}
	}
	return snap, rows.Err()
}
