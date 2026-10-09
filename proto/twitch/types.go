// Package twitch defines the collector's HTTP contract. It contains no credentials.
package twitch

import (
	"fmt"
	"math"
	"terex/proto/msg"
	"time"
	_ "time/tzdata"
)

type Settings struct {
	Enabled                  bool    `json:"enabled"`
	TargetAccountID          int64   `json:"target_account_id"`
	PollIntervalMinutes      int     `json:"poll_interval_minutes"`
	WindowHours              int     `json:"window_hours"`
	First                    int     `json:"first"`
	MinViews                 int     `json:"min_views"`
	MinAgeMinutes            int     `json:"min_age_minutes"`
	MinDurationSec           float64 `json:"min_duration_sec"`
	MaxDurationSec           float64 `json:"max_duration_sec"`
	DailyDownloads           int     `json:"daily_downloads"`
	CompilationBudgetPercent int     `json:"compilation_budget_percent"`
	AdmissionMode            string  `json:"admission_mode"`
	PerChannelDailyDownloads int     `json:"per_channel_daily_downloads"`
	DailyShorts              int     `json:"daily_shorts"`
	DailyCompilations        int     `json:"daily_compilations"`
	CompilationMinClips      int     `json:"compilation_min_clips"`
	CompilationMaxClips      int     `json:"compilation_max_clips"`
	CompilationHour          int     `json:"compilation_hour"`
	BacklogLimit             int     `json:"backlog_limit"`
	Timezone                 string  `json:"timezone"`
}

func DefaultSettings() Settings {
	return Settings{PollIntervalMinutes: 144, WindowHours: 72, First: 100, MinViews: 100, MinAgeMinutes: 60,
		MinDurationSec: 5, MaxDurationSec: 60, DailyDownloads: 10, CompilationBudgetPercent: 70, AdmissionMode: "shadow", PerChannelDailyDownloads: 2,
		DailyShorts: 5, DailyCompilations: 1, CompilationMinClips: 3, CompilationMaxClips: 10,
		CompilationHour: 21, BacklogLimit: 40, Timezone: "Asia/Tomsk"}
}

// MaxClipDurationSec is the longest source clip the pipeline accepts. Twitch
// reports some clips slightly over 60 s; 180 s is the YouTube Shorts limit.
const MaxClipDurationSec = 180

func (s Settings) Validate() error {
	if s.CompilationBudgetPercent < 0 || s.CompilationBudgetPercent > 100 || (s.AdmissionMode != "" && s.AdmissionMode != "shadow" && s.AdmissionMode != "enforce") {
		return fmt.Errorf("invalid preparation allocation settings")
	}
	if s.PollIntervalMinutes < 1 || s.PollIntervalMinutes > 10080 || s.WindowHours < 1 || s.WindowHours > 720 || s.First < 1 || s.First > 100 || s.MinViews < 0 || s.MinAgeMinutes < 0 || s.MinAgeMinutes >= s.WindowHours*60 {
		return fmt.Errorf("invalid discovery limits")
	}
	if math.IsNaN(s.MinDurationSec) || math.IsNaN(s.MaxDurationSec) || math.IsInf(s.MinDurationSec, 0) || math.IsInf(s.MaxDurationSec, 0) || s.MinDurationSec <= 0 || s.MaxDurationSec < s.MinDurationSec || s.MaxDurationSec > MaxClipDurationSec {
		return fmt.Errorf("duration must be between 0 and %d seconds", MaxClipDurationSec)
	}
	if s.DailyDownloads < 1 || s.DailyDownloads > 100 || s.PerChannelDailyDownloads < 1 || s.PerChannelDailyDownloads > s.DailyDownloads || s.DailyShorts < 1 || s.DailyShorts > 100 || s.DailyCompilations < 1 || s.DailyCompilations > 10 || s.BacklogLimit < 1 || s.BacklogLimit > 1000 {
		return fmt.Errorf("invalid daily or backlog limits")
	}
	if s.CompilationMinClips < 3 || s.CompilationMaxClips < s.CompilationMinClips || s.CompilationMaxClips > 10 || s.CompilationHour < 0 || s.CompilationHour > 23 {
		return fmt.Errorf("invalid compilation limits")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("invalid timezone")
	}
	return nil
}

type Source struct {
	Platform         string      `json:"platform"`
	ClipID           string      `json:"clip_id"`
	BroadcasterID    string      `json:"broadcaster_id"`
	BroadcasterLogin string      `json:"broadcaster_login"`
	Title            string      `json:"title"`
	URL              string      `json:"url"`
	CreatedAt        time.Time   `json:"created_at"`
	ObservedAt       time.Time   `json:"observed_at"`
	ViewCount        int         `json:"view_count"`
	DurationSec      float64     `json:"duration_sec"`
	VideoID          string      `json:"video_id,omitempty"`
	VODOffset        *float64    `json:"vod_offset,omitempty"`
	GameID           string      `json:"game_id,omitempty"`
	Language         string      `json:"language,omitempty"`
	LanguageSource   string      `json:"language_source,omitempty"`
	ThumbnailURL     string      `json:"thumbnail_url,omitempty"`
	Growth           *ClipGrowth `json:"growth,omitempty"`
}

// Flow pins one publication destination. Discovery is shared; output decisions
// and scheduling belong to the flow rather than to the physical media file.
type Flow struct {
	ID                        string     `json:"id"`
	Name                      string     `json:"name"`
	Enabled                   bool       `json:"enabled"`
	AutoPublish               bool       `json:"auto_publish"`
	Archived                  bool       `json:"archived"`
	ScheduleHorizonHours      int        `json:"schedule_horizon_hours"`
	MaxSourceAgeHours         int        `json:"max_source_age_hours"`
	AccountID                 int64      `json:"account_id"`
	YouTubeChannelID          string     `json:"youtube_channel_id"`
	Version                   int        `json:"version"`
	SourceMode                string     `json:"source_mode"`
	BroadcasterIDs            []string   `json:"broadcaster_ids"`
	ChannelInputs             []string   `json:"channel_inputs,omitempty"`
	ExcludedSourceIDs         []string   `json:"excluded_source_ids,omitempty"`
	CategoryID                string     `json:"category_id,omitempty"`
	CategoryName              string     `json:"category_name,omitempty"`
	Language                  string     `json:"language,omitempty"`
	RankingMode               string     `json:"ranking_mode"`
	PollIntervalMinutes       int        `json:"poll_interval_minutes"`
	WindowHours               int        `json:"window_hours"`
	MinViews                  int        `json:"min_views"`
	MinAgeMinutes             int        `json:"min_age_minutes"`
	MinDurationSec            float64    `json:"min_duration_sec"`
	MaxDurationSec            float64    `json:"max_duration_sec"`
	ShortsEnabled             bool       `json:"shorts_enabled"`
	CompilationsEnabled       bool       `json:"compilations_enabled"`
	CompilationMinDurationSec float64    `json:"compilation_min_duration_sec"`
	CompilationMaxDurationSec float64    `json:"compilation_max_duration_sec"`
	CompilationMinClips       int        `json:"compilation_min_clips"`
	CompilationMaxClips       int        `json:"compilation_max_clips"`
	CompilationHour           int        `json:"compilation_hour"`
	BalanceByTime             bool       `json:"balance_by_time"`
	ShortsSlots               []string   `json:"shorts_slots"`
	CompilationSlots          []string   `json:"compilation_slots"`
	Timezone                  string     `json:"timezone"`
	DailyShorts               int        `json:"daily_shorts"`
	DailyCompilations         int        `json:"daily_compilations"`
	LegacyLength              bool       `json:"legacy_length,omitempty"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
	AutoEnabledAt             *time.Time `json:"auto_enabled_at,omitempty"`
	WorkStartedAt             *time.Time `json:"work_started_at,omitempty"`
	LastCheckedAt             *time.Time `json:"last_checked_at,omitempty"`
	BlockReason               string     `json:"block_reason,omitempty"`
	FoundClips                int        `json:"found_clips"`
	ReadyOutputs              int        `json:"ready_outputs"`
	NextPublicationAt         *time.Time `json:"next_publication_at,omitempty"`
}

type CollectorConfig struct {
	Epoch            int64             `json:"epoch"`
	Maintenance      *ResetMaintenance `json:"maintenance,omitempty"`
	Settings         Settings          `json:"settings"`
	Flows            []Flow            `json:"flows"`
	BlockedSourceIDs []string          `json:"blocked_source_ids,omitempty"`
}

// Capacity is advisory. Import reservation repeats admission in the database
// transaction so collectors cannot overbook a shared YouTube destination.
type FlowCapacity struct {
	FlowID           string           `json:"flow_id"`
	YouTubeChannelID string           `json:"youtube_channel_id"`
	HorizonHours     int              `json:"horizon_hours"`
	HorizonEnd       time.Time        `json:"horizon_end"`
	Mode             string           `json:"mode"`
	Formats          []FormatCapacity `json:"formats"`
}
type FormatCapacity struct {
	Format             string     `json:"format"`
	TotalSlots         int        `json:"total_slots"`
	OccupiedSlots      int        `json:"occupied_slots"`
	PreparingSlots     int        `json:"preparing_slots"`
	FreeSlots          int        `json:"free_slots"`
	NextSlotAt         *time.Time `json:"next_slot_at,omitempty"`
	Reason             string     `json:"reason,omitempty"`
	NextCheckAt        time.Time  `json:"next_check_at"`
	SourceCount        int        `json:"source_count"`
	SourceDurationSec  float64    `json:"source_duration_sec"`
	MissingClips       int        `json:"missing_clips"`
	MissingDurationSec float64    `json:"missing_duration_sec"`
}
type PreparationWait struct {
	Code        string    `json:"code"`
	Message     string    `json:"message"`
	NextCheckAt time.Time `json:"next_check_at"`
}

type ObservationRequest struct {
	Epoch       int64    `json:"epoch"`
	FlowID      string   `json:"flow_id"`
	FlowVersion int      `json:"flow_version"`
	Sources     []Source `json:"sources"`
}

type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ImportRequest struct {
	AdmissionSequence int64  `json:"admission_sequence,omitempty"`
	Epoch             int64  `json:"epoch"`
	SchemaVersion     int    `json:"schema_version"`
	IdempotencyKey    string `json:"idempotency_key"`
	Source            Source `json:"source"`
	AccountID         int64  `json:"account_id,omitempty"`
	FlowID            string `json:"flow_id,omitempty"`
	FlowVersion       int    `json:"flow_version,omitempty"`
	ReservationID     string `json:"reservation_id,omitempty"`
	UploadGeneration  int64  `json:"upload_generation,omitempty"`
}
type ImportCompleteRequest struct {
	Epoch            int64  `json:"epoch"`
	SHA256           string `json:"sha256"`
	SizeBytes        int64  `json:"size_bytes"`
	UploadGeneration int64  `json:"upload_generation,omitempty"`
}
type Import struct {
	AdmissionSequence int64         `json:"admission_sequence,omitempty"`
	RevalidationToken string        `json:"revalidation_token,omitempty"`
	Epoch             int64         `json:"epoch"`
	PreparationFormat string        `json:"preparation_format,omitempty"`
	ImportID          string        `json:"import_id"`
	Source            Source        `json:"source"`
	State             string        `json:"state"`
	AccountID         int64         `json:"account_id"`
	JobID             string        `json:"job_id,omitempty"`
	ClipID            string        `json:"clip_id,omitempty"`
	RevisionID        int64         `json:"revision_id,omitempty"`
	UploadURL         string        `json:"upload_url,omitempty"`
	UploadExpiresAt   *time.Time    `json:"upload_expires_at,omitempty"`
	Original          msg.ObjectRef `json:"original,omitempty"`
	SHA256            string        `json:"sha256,omitempty"`
	DurationSec       float64       `json:"duration_sec,omitempty"`
	SizeBytes         int64         `json:"size_bytes,omitempty"`
	Width             int           `json:"width,omitempty"`
	Height            int           `json:"height,omitempty"`
	Rejected          bool          `json:"rejected"`
	Excluded          bool          `json:"excluded"`
	CreatedAt         time.Time     `json:"created_at"`
	Error             string        `json:"error,omitempty"`
	FlowID            string        `json:"flow_id,omitempty"`
	FlowVersion       int           `json:"flow_version,omitempty"`
	ReservationID     string        `json:"reservation_id,omitempty"`
	MediaID           string        `json:"media_id,omitempty"`
	MediaState        string        `json:"media_state,omitempty"`
	UploadGeneration  int64         `json:"upload_generation,omitempty"`
	UploadImportID    string        `json:"upload_import_id,omitempty"`
}
type CompilationItemRequest struct {
	ClipID     string  `json:"clip_id"`
	RevisionID int64   `json:"revision_id"`
	StartSec   float64 `json:"start_sec"`
	EndSec     float64 `json:"end_sec"`
	SourceID   string  `json:"source_id,omitempty"`
}
type CompilationRequest struct {
	Title  string                   `json:"title"`
	Items  []CompilationItemRequest `json:"items"`
	FlowID string                   `json:"flow_id,omitempty"`
}
