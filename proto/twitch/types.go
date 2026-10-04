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
		MinDurationSec: 5, MaxDurationSec: 60, DailyDownloads: 10, PerChannelDailyDownloads: 2,
		DailyShorts: 5, DailyCompilations: 1, CompilationMinClips: 3, CompilationMaxClips: 10,
		CompilationHour: 21, BacklogLimit: 40, Timezone: "Asia/Tomsk"}
}
func (s Settings) Validate() error {
	if s.Enabled && s.TargetAccountID <= 0 {
		return fmt.Errorf("select a target YouTube account before enabling")
	}
	if s.PollIntervalMinutes < 1 || s.PollIntervalMinutes > 10080 || s.WindowHours < 1 || s.WindowHours > 720 || s.First < 1 || s.First > 100 || s.MinViews < 0 || s.MinAgeMinutes < 0 || s.MinAgeMinutes >= s.WindowHours*60 {
		return fmt.Errorf("invalid discovery limits")
	}
	if math.IsNaN(s.MinDurationSec) || math.IsNaN(s.MaxDurationSec) || math.IsInf(s.MinDurationSec, 0) || math.IsInf(s.MaxDurationSec, 0) || s.MinDurationSec <= 0 || s.MaxDurationSec < s.MinDurationSec || s.MaxDurationSec > 60 {
		return fmt.Errorf("duration must be between 0 and 60 seconds")
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
	Platform         string    `json:"platform"`
	ClipID           string    `json:"clip_id"`
	BroadcasterID    string    `json:"broadcaster_id"`
	BroadcasterLogin string    `json:"broadcaster_login"`
	Title            string    `json:"title"`
	URL              string    `json:"url"`
	CreatedAt        time.Time `json:"created_at"`
	ObservedAt       time.Time `json:"observed_at"`
	ViewCount        int       `json:"view_count"`
	DurationSec      float64   `json:"duration_sec"`
	VideoID          string    `json:"video_id,omitempty"`
	VODOffset        *float64  `json:"vod_offset,omitempty"`
}
type ImportRequest struct {
	SchemaVersion  int    `json:"schema_version"`
	IdempotencyKey string `json:"idempotency_key"`
	Source         Source `json:"source"`
	AccountID      int64  `json:"account_id,omitempty"`
}
type ImportCompleteRequest struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}
type Import struct {
	ImportID        string        `json:"import_id"`
	Source          Source        `json:"source"`
	State           string        `json:"state"`
	AccountID       int64         `json:"account_id"`
	JobID           string        `json:"job_id,omitempty"`
	ClipID          string        `json:"clip_id,omitempty"`
	RevisionID      int64         `json:"revision_id,omitempty"`
	UploadURL       string        `json:"upload_url,omitempty"`
	UploadExpiresAt *time.Time    `json:"upload_expires_at,omitempty"`
	Original        msg.ObjectRef `json:"original,omitempty"`
	SHA256          string        `json:"sha256,omitempty"`
	DurationSec     float64       `json:"duration_sec,omitempty"`
	SizeBytes       int64         `json:"size_bytes,omitempty"`
	Width           int           `json:"width,omitempty"`
	Height          int           `json:"height,omitempty"`
	Rejected        bool          `json:"rejected"`
	CreatedAt       time.Time     `json:"created_at"`
	Error           string        `json:"error,omitempty"`
}
type CompilationItemRequest struct {
	ClipID     string  `json:"clip_id"`
	RevisionID int64   `json:"revision_id"`
	StartSec   float64 `json:"start_sec"`
	EndSec     float64 `json:"end_sec"`
}
type CompilationRequest struct {
	Title string                   `json:"title"`
	Items []CompilationItemRequest `json:"items"`
}
