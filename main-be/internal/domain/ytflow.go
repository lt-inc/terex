package domain

import (
	"context"
	"time"
)

// Candidate states. new = waiting for a slot; queued = a job was created for a reserved slot.
const (
	YTNew     = "new"
	YTQueued  = "queued"
	YTDone    = "done"
	YTFailed  = "failed"
	YTSkipped = "skipped"

	YTSourceChannel = "channel"
	YTSourceSearch  = "search"

	// YTSourceKind / YTOutputFormat mark the jobs of YouTube flows: the format is what makes their
	// publications count against the same daily Shorts limit as Twitch flows on that channel.
	YTJobSourceKind = "youtube_flow"
	YTOutputFormat  = "short"
)

// YTFlowConfig is stored as JSON; zero values are replaced by defaults in the service.
type YTFlowConfig struct {
	WindowHours     int      `json:"window_hours"`       // only videos published within this window are considered
	AllTime         bool     `json:"all_time"`           // ignore the window: take the whole channel history, best by total views
	MaxVideos       int      `json:"max_videos"`         // all-time scan depth per channel (Shorts, newest first)
	MinViews        int64    `json:"min_views"`          // absolute floor
	MinViewsPerHour float64  `json:"min_views_per_hour"` // growth floor, 0 = off
	MinDurationSec  int      `json:"min_duration_sec"`
	MaxDurationSec  int      `json:"max_duration_sec"`
	Slots           []string `json:"slots"`            // local HH:MM publication windows
	DailyLimit      int      `json:"daily_limit"`      // this flow's own cap per local day, 0 = only the channel limit
	DailyDownloads  int      `json:"daily_downloads"`  // jobs this flow may create per local day, 0 = unlimited
	ApproveMode     string   `json:"approve_mode"`     // auto_approve | by_approve
	QuotaDaily      int      `json:"quota_daily"`      // YouTube API units per Pacific day, 0 = the shared pool only
	RefreshHours    int      `json:"refresh_hours"`    // how often statistics of waiting candidates are re-read
	ScanHours       int      `json:"scan_hours"`       // full rescan of a source (Shorts playlist / search)
	BrandStyle      bool     `json:"brand_style"`      // render with the branded layout (msg.BrandOptions)
	BrandBackground string   `json:"brand_background"` // background asset name; empty = blur of the video
	BrandBanner     string   `json:"brand_banner"`     // banner asset name; empty = no banner
}

type YTSourceParams struct {
	RegionCode string `json:"region_code,omitempty"`
	Language   string `json:"language,omitempty"`
}

type YTSource struct {
	ID         int64          `json:"id"`
	FlowID     string         `json:"flow_id"`
	Kind       string         `json:"kind"`
	Value      string         `json:"value"` // channel id (UC...) or search query
	Title      string         `json:"title"`
	Params     YTSourceParams `json:"params"`
	LastRSSAt  *time.Time     `json:"last_rss_at"`
	LastScanAt *time.Time     `json:"last_scan_at"`
	LastError  string         `json:"last_error,omitempty"`
}

// YTFlowCounts are candidate counters shown on the flow card.
type YTFlowCounts struct {
	New     int `json:"new"`
	Queued  int `json:"queued"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

type YTFlow struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	AccountID   int64        `json:"account_id"`
	AccountName string       `json:"account_name"`
	ChannelID   string       `json:"channel_id"` // destination YouTube channel
	Enabled     bool         `json:"enabled"`
	Config      YTFlowConfig `json:"config"`
	Version     int          `json:"version"`
	LastRunAt   *time.Time   `json:"last_run_at"`
	LastStatus  string       `json:"last_status"`
	LastError   string       `json:"last_error,omitempty"`
	Sources     []YTSource   `json:"sources"`
	Counts      YTFlowCounts `json:"counts"`
	QuotaUsed   int          `json:"quota_used"` // units spent today (Pacific day)
	NextSlotAt  *time.Time   `json:"next_slot_at"`
}

type YTCandidate struct {
	FlowID       string     `json:"flow_id"`
	VideoID      string     `json:"video_id"`
	SourceID     *int64     `json:"source_id"`
	ChannelID    string     `json:"channel_id"`
	ChannelTitle string     `json:"channel_title"`
	Title        string     `json:"title"`
	PublishedAt  *time.Time `json:"published_at"`
	DurationSec  int        `json:"duration_sec"`
	Views        int64      `json:"views"`
	Likes        *int64     `json:"likes"`
	FirstSeenAt  time.Time  `json:"first_seen_at"`
	StatsAt      *time.Time `json:"stats_at"`
	State        string     `json:"state"`
	Reason       string     `json:"reason,omitempty"`
	JobID        *string    `json:"job_id"`
	SlotAt       *time.Time `json:"slot_at"`
	Score        float64    `json:"score"` // views per hour since publication
}

// Reasons ReserveYTSlots gives when it reserved nothing.
const (
	YTWaitNoCandidates = "no_candidates" // nothing passes the filters
	YTWaitNoSlots      = "no_free_slots" // the channel calendar (shared with Twitch) has no free window
	YTWaitFlowLimit    = "flow_limit"    // this flow's own daily limits are reached
)

// YTReservation is a candidate bound to a free slot of the shared calendar.
type YTReservation struct {
	Candidate YTCandidate
	SlotAt    time.Time
	Timezone  string
}

// YTReader is a connected YouTube account whose token gpu-be uses to read public data.
type YTReader struct {
	AccountID     int64
	Name          string
	ChannelID     string
	CredentialRef ObjectRef
}

type ObjectRef struct {
	Bucket string `json:"bucket"`
	Object string `json:"object"`
}

type YTQuotaReport struct {
	Day      string         `json:"day"` // Pacific date
	Pool     int            `json:"pool"`
	Used     int            `json:"used"`
	ByFlow   map[string]int `json:"by_flow"`
	Tripped  bool           `json:"tripped"`
	TripNote string         `json:"trip_note,omitempty"`
}

// YTFlowRepo is implemented by repository/postgres.
type YTFlowRepo interface {
	ListYTFlows(ctx context.Context, day time.Time) ([]YTFlow, error)
	GetYTFlow(ctx context.Context, id string, day time.Time) (YTFlow, error)
	SaveYTFlow(ctx context.Context, f YTFlow, create bool) (YTFlow, error)
	DeleteYTFlow(ctx context.Context, id string) error
	AddYTSource(ctx context.Context, s YTSource) (YTSource, error)
	DeleteYTSource(ctx context.Context, flowID string, id int64) error
	TouchYTSource(ctx context.Context, id int64, rss, scan *time.Time, lastError string) error
	MarkYTFlowRun(ctx context.Context, id string, at time.Time, status, errText string) error

	YTKnownVideos(ctx context.Context, flowID string, ids []string) (map[string]bool, error)
	UpsertYTCandidates(ctx context.Context, flowID string, sourceID *int64, c []YTCandidate, now time.Time) error
	YTCandidatesToRefresh(ctx context.Context, flowID string, statsBefore, publishedAfter time.Time, limit int) ([]string, error)
	ListYTCandidates(ctx context.Context, flowID, state string, limit int) ([]YTCandidate, error)
	SkipYTCandidate(ctx context.Context, flowID, videoID, reason string) error

	// ReserveYTSlots binds the best waiting candidates to free slots of the shared calendar under the
	// scheduling lock; the returned reservations are already visible to the Twitch scheduler.
	ReserveYTSlots(ctx context.Context, flowID string, now time.Time) ([]YTReservation, string, error)
	AttachYTJob(ctx context.Context, flowID, videoID, jobID string) error
	ReleaseYTReservation(ctx context.Context, flowID, videoID, reason string, permanent bool) error
	SyncYTCandidates(ctx context.Context) error
	YTClipTitle(ctx context.Context, jobID string) (title string, ok bool, err error)

	YTReaders(ctx context.Context) ([]YTReader, error)
	// ReserveYTQuota atomically adds units for a flow unless that exceeds the flow budget or the shared pool.
	ReserveYTQuota(ctx context.Context, day time.Time, flowID string, units, flowLimit, pool int) (bool, error)
	AdjustYTQuota(ctx context.Context, day time.Time, flowID string, delta int) error
	YTQuotaReport(ctx context.Context, day time.Time, pool int) (YTQuotaReport, error)
	TripYTBreaker(ctx context.Context, day time.Time, message string) error
	YTBreakerTripped(ctx context.Context, day time.Time) (bool, error)
}
