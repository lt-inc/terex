package domain

import (
	"context"
	"errors"
	"terex/proto/twitch"
	"time"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrLeaseLost = errors.New("download lease lost")
	ErrPaused    = errors.New("collector or channel paused")
)

type Settings = twitch.Settings

type Channel struct {
	ID            string     `json:"id"`
	BroadcasterID string     `json:"broadcaster_id"`
	Login         string     `json:"login"`
	DisplayName   string     `json:"display_name"`
	Paused        bool       `json:"paused"`
	Archived      bool       `json:"archived"`
	CreatedAt     time.Time  `json:"created_at"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

type Clip struct {
	ID              string             `json:"id"`
	ChannelID       string             `json:"channel_id"`
	BroadcasterID   string             `json:"broadcaster_id"`
	BroadcasterName string             `json:"broadcaster_name"`
	Title           string             `json:"title"`
	ViewCount       int                `json:"view_count"`
	CreatedAt       time.Time          `json:"created_at"`
	Duration        float64            `json:"duration"`
	VideoID         string             `json:"video_id,omitempty"`
	VODOffset       *float64           `json:"vod_offset,omitempty"`
	URL             string             `json:"url"`
	State           string             `json:"state"`
	LastError       string             `json:"last_error,omitempty"`
	ImportID        string             `json:"import_id,omitempty"`
	SHA256          string             `json:"sha256,omitempty"`
	GameID          string             `json:"game_id,omitempty"`
	Language        string             `json:"language,omitempty"`
	LanguageSource  string             `json:"language_source,omitempty"`
	ThumbnailURL    string             `json:"thumbnail_url,omitempty"`
	FlowID          string             `json:"flow_id,omitempty"`
	FlowVersion     int                `json:"flow_version,omitempty"`
	ObservedAt      time.Time          `json:"observed_at,omitempty"`
	Growth          *twitch.ClipGrowth `json:"growth,omitempty"`
}

type Task struct {
	Epoch       int64
	Clip        Clip
	Token       string
	Attempt     int
	FlowID      string
	FlowVersion int
	Reservation twitch.Import
	Uploaded    *UploadedMedia
}

type FailedImport struct {
	ClipID       string
	Reservation  twitch.Import
	Reason       string
	FailedAt     time.Time
	FailureToken string
	ErrorCode    string
	FlowID       string
	FlowVersion  int
	Uploaded     *UploadedMedia
}

type ClipStatus struct {
	ClipID        string     `json:"clip_id"`
	State         string     `json:"state"`
	Reason        string     `json:"reason,omitempty"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	Attempt       int        `json:"attempt"`
}

// DiscoveryReport describes a bounded metadata search, never a download queue.
type DiscoveryReport struct {
	FlowID           string                    `json:"flow_id"`
	BroadcasterID    string                    `json:"broadcaster_id,omitempty"`
	Language         string                    `json:"language,omitempty"`
	Fetched          int                       `json:"fetched"`
	LanguageMatched  int                       `json:"language_matched"`
	LanguageRejected int                       `json:"language_rejected"`
	LanguageUnknown  int                       `json:"language_unknown"`
	Pages            int                       `json:"pages"`
	PageLimitReached bool                      `json:"page_limit_reached"`
	CheckedAt        time.Time                 `json:"checked_at"`
	Error            string                    `json:"error,omitempty"`
	Coverage         *twitch.DiscoveryCoverage `json:"coverage,omitempty"`
}

// Flow interfaces keep the v1 adapter available during a rolling deployment.
type FlowTerex interface {
	CollectorConfig(context.Context) (twitch.CollectorConfig, error)
	Observe(context.Context, twitch.ObservationRequest) ([]twitch.Import, error)
	ReserveImport(context.Context, Task) (twitch.Import, error)
	UploadImport(context.Context, twitch.Import, Download, func(context.Context) error) error
}
type FlowStore interface {
	SyncFlows(context.Context, []twitch.Flow) error
	LegacyBroadcasterIDs(context.Context) ([]string, error)
	ObserveFlow(context.Context, twitch.Flow, []Clip) error
	SharedReady(context.Context, []twitch.Import) error
	SaveReservation(context.Context, Task, twitch.Import) error
	HasActiveConsumer(context.Context, string) (bool, error)
	PinActiveConsumer(context.Context, Task) (Task, error)
}
type FlowTwitch interface {
	ResolveBroadcaster(context.Context, string) (Channel, error)
	GetFlowClips(context.Context, twitch.Flow, Channel, time.Time, time.Time, int, map[string]bool) ([]Clip, error)
	Categories(context.Context, string) ([]twitch.Category, error)
}

type Status struct {
	Runtime          *twitch.RuntimeWork `json:"runtime,omitempty"`
	Running          bool                `json:"running"`
	Paused           bool                `json:"paused"`
	CredentialsReady bool                `json:"credentials_ready"`
	LastCheckAt      *time.Time          `json:"last_check_at,omitempty"`
	LastError        string              `json:"last_error,omitempty"`
	BlockReason      string              `json:"block_reason,omitempty"`
	Channels         int                 `json:"channels"`
	Queued           int                 `json:"queued"`
	Downloading      int                 `json:"downloading"`
	Imported         int                 `json:"imported"`
	Failed           int                 `json:"failed"`
}

type Download struct {
	Path          string
	Size          int64
	SHA256        string
	Duration      float64
	Width         int
	Height        int
	HasAudio      bool
	Cleanup       func()
	ProbeDeferred bool
}

type Failure struct {
	Kind        string
	Message     string
	Retryable   bool
	RetryAfter  time.Duration
	HTTPStatus  int
	Code        string
	NextCheckAt *time.Time
}

func (e *Failure) Error() string { return e.Message }

// StorageError identifies the failed operation without including connection
// strings or query values. Unwrap preserves PostgreSQL diagnostics for logging.
type StorageError struct {
	Operation string
	Err       error
}

func (e *StorageError) Error() string { return "collector storage operation failed: " + e.Operation }
func (e *StorageError) Unwrap() error { return e.Err }

type Store interface {
	Migrate(context.Context) error
	ListChannels(context.Context) ([]Channel, error)
	CreateChannel(context.Context, Channel) (Channel, error)
	SetChannel(context.Context, string, *bool, bool) (Channel, error)
	GetChannel(context.Context, string) (Channel, error)
	ChannelChecked(context.Context, string, time.Time, string) error
	UpsertClips(context.Context, []Clip) error
	ListClips(context.Context, int, string) ([]Clip, error)
	Reserve(context.Context, []Clip, Settings, time.Time) (int, error)
	Claim(context.Context, time.Duration) (*Task, error)
	Renew(context.Context, Task, time.Duration) error
	ValidLease(context.Context, Task) error
	Complete(context.Context, Task, string, string) error
	Fail(context.Context, Task, string, string, *time.Time) error
	Defer(context.Context, Task, time.Time) error
	HasSHA256(context.Context, string, string) (bool, error)
	SetLastCheck(context.Context, time.Time, string) error
	Status(context.Context) (Status, error)
}

type Twitch interface {
	ResolveChannel(context.Context, string) (Channel, error)
	GetClips(context.Context, Channel, time.Time, time.Time, int) ([]Clip, error)
	Ready() bool
}

type Terex interface {
	Settings(context.Context) (Settings, error)
	PendingCount(context.Context) (int, error)
	Import(context.Context, Clip, Download, int64, func(context.Context) error) (string, error)
}

type Downloader interface {
	Download(context.Context, string, float64) (*Download, error)
}
