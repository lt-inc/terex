// Package domain holds the news collector entities. Flows connect a topic, a set of
// sources and one Telegram channel; posts are the generated drafts that end up there.
package domain

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
	ErrConflict = errors.New("conflict")
)

const (
	SourceRSS   = "rss"   // any RSS/Atom feed
	SourceGNews = "gnews" // Google News search feed built from a query
)

const (
	ApproveManual = "by_approve"
	ApproveAuto   = "auto"
)

const (
	ImageNone    = "none"    // text only
	ImageArticle = "article" // the picture published with the news itself
	ImageWeb     = "web"     // a stock photo found by topic (Pexels)
	ImageAuto    = "auto"    // the article's picture, else a stock photo
)

const (
	PostPending   = "pending"
	PostApproved  = "approved"
	PostRejected  = "rejected"
	PostPublished = "published"
	PostFailed    = "failed"
	PostExpired   = "expired"
)

type Source struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Kind             string     `json:"kind"`
	URL              string     `json:"url"`
	Query            string     `json:"query"`
	Lang             string     `json:"lang"`
	Enabled          bool       `json:"enabled"`
	FetchIntervalMin int        `json:"fetch_interval_min"`
	LastFetchedAt    *time.Time `json:"last_fetched_at"`
	LastError        string     `json:"last_error"`
	Articles         int        `json:"articles"`
	CreatedAt        time.Time  `json:"created_at"`
}

// Channel is a Telegram channel (or group) the bot publishes to. The bot token is
// write-only: it is stored encrypted and never returned.
type Channel struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	ChatID      string     `json:"chat_id"`
	BotUsername string     `json:"bot_username"`
	ChatTitle   string     `json:"chat_title"`
	Status      string     `json:"status"` // unverified | ok | error
	LastError   string     `json:"last_error"`
	VerifiedAt  *time.Time `json:"verified_at"`
	HasToken    bool       `json:"has_token"`
	CreatedAt   time.Time  `json:"created_at"`
	// BotToken is accepted on input only.
	BotToken string `json:"bot_token,omitempty"`
	TokenEnc []byte `json:"-"`
}

type Flow struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Enabled        bool       `json:"enabled"`
	Topic          string     `json:"topic"`
	Language       string     `json:"language"` // language of generated posts: ru | en
	ChannelID      *int64     `json:"channel_id"`
	ApproveMode    string     `json:"approve_mode"`
	PostsPerDay    int        `json:"posts_per_day"`
	WindowStart    int        `json:"window_start_hour"`
	WindowEnd      int        `json:"window_end_hour"`
	Timezone       string     `json:"timezone"`
	MinIntervalMin int        `json:"min_interval_min"`
	MinSources     int        `json:"min_sources"`
	MinRelevance   float64    `json:"min_relevance"`
	WindowHours    int        `json:"window_hours"`
	MaxPending     int        `json:"max_pending"`
	StylePrompt    string     `json:"style_prompt"`
	IncludeLink    bool       `json:"include_link"`
	MaxChars       int        `json:"max_chars"`
	Model          string     `json:"model"`
	ImageMode      string     `json:"image_mode"`
	UseEmoji       bool       `json:"use_emoji"`
	UseHashtags    bool       `json:"use_hashtags"`
	Footer         string     `json:"footer"`
	SourceIDs      []int64    `json:"source_ids"`
	LastGenerated  *time.Time `json:"last_generated_at"`
	LastError      string     `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`

	TopicEmbedding []float32 `json:"-"`
	// Read-only counters filled by list queries.
	ChannelTitle string `json:"channel_title,omitempty"`
	Pending      int    `json:"pending_posts"`
	PostedToday  int    `json:"posted_today"`
	OpenClusters int    `json:"open_clusters"`
}

type Article struct {
	ID          int64     `json:"id"`
	SourceID    int64     `json:"source_id"`
	SourceName  string    `json:"source_name"`
	Publisher   string    `json:"publisher"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	Text        string    `json:"text,omitempty"`
	Summary     string    `json:"-"`
	Lang        string    `json:"lang"`
	PublishedAt time.Time `json:"published_at"`
	Status      string    `json:"status"` // new | extracted | ready | duplicate | failed
	SimHash     uint64    `json:"-"`
	Embedding   []float32 `json:"-"`
	DupOf       *int64    `json:"dup_of"`
	ImageURL    string    `json:"image_url"`
}

type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type Post struct {
	ID             int64      `json:"id"`
	FlowID         int64      `json:"flow_id"`
	FlowName       string     `json:"flow_name,omitempty"`
	ClusterID      *int64     `json:"cluster_id"`
	ChannelID      *int64     `json:"channel_id"`
	Title          string     `json:"title"`
	Text           string     `json:"text"`
	Links          []Link     `json:"links"`
	Status         string     `json:"status"`
	ScheduledAt    *time.Time `json:"scheduled_at"`
	PublishedAt    *time.Time `json:"published_at"`
	MessageID      *int64     `json:"message_id"`
	Error          string     `json:"error"`
	Attempts       int        `json:"attempts"`
	Score          float64    `json:"score"`
	Emoji          string     `json:"emoji"`
	Hashtags       string     `json:"hashtags"` // "#tag #other", rendered after the text
	ImageURL       string     `json:"image_url"`
	ImageSource    string     `json:"image_source"` // article | pexels | manual | ""
	ImageCredit    string     `json:"image_credit"`
	ImageCreditURL string     `json:"image_credit_url"`
	ImageQuery     string     `json:"image_query"` // English stock-photo search phrase proposed by the model
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// Cluster is one story: articles about the same event, within one flow.
type Cluster struct {
	ID        int64     `json:"id"`
	FlowID    int64     `json:"flow_id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"` // open | used | skipped
	Centroid  []float32 `json:"-"`
	Relevance float64   `json:"relevance"`
	Sources   int       `json:"sources"`
	Articles  int       `json:"articles"`
	FirstAt   time.Time `json:"first_at"`
	LastAt    time.Time `json:"last_at"`
	Score     float64   `json:"score"`
}

type Status struct {
	LLMReady       bool       `json:"llm_ready"`
	StockReady     bool       `json:"stock_ready"`
	SecretKeyReady bool       `json:"secret_key_ready"`
	Flows          int        `json:"flows"`
	EnabledFlows   int        `json:"enabled_flows"`
	Sources        int        `json:"sources"`
	Channels       int        `json:"channels"`
	Articles24h    int        `json:"articles_24h"`
	Unembedded     int        `json:"unembedded"`
	PendingPosts   int        `json:"pending_posts"`
	LastFetchAt    *time.Time `json:"last_fetch_at"`
	LastTickAt     *time.Time `json:"last_tick_at"`
	LastTickError  string     `json:"last_tick_error"`
}
