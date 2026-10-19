package domain

import "time"

const (
	ApproveBy   = "by_approve"
	ApproveAuto = "auto_approve"

	ClipPending  = "pending"
	ClipApproved = "approved"
	ClipRejected = "rejected"

	PubScheduled = "scheduled"
	PubSent      = "sent"
	PubPublished = "published"
	PubFailed    = "failed"
)

// Asset kinds stored as simple name+file records.
type AssetKind string

const (
	KindBanner     AssetKind = "banners"
	KindBackground AssetKind = "backgrounds"
)

type Account struct {
	ID                  int64      `json:"id"`
	Platform            string     `json:"platform"`
	Name                string     `json:"name"`
	URL                 *string    `json:"url"`
	Note                *string    `json:"note"`
	Bucket              *string    `json:"bucket"`
	FileName            *string    `json:"file_name"`
	ConnectionState     string     `json:"connection_state"`
	RemoteAccountID     *string    `json:"remote_account_id"`
	RemoteAccountName   *string    `json:"remote_account_name"`
	ConnectionError     *string    `json:"connection_error"`
	ConnectionCheckedAt *time.Time `json:"last_checked_at"`
	ConnectionScopes    []string   `json:"scopes"`
	CredentialExpiresAt *time.Time `json:"expires_at"`
	ConnectionVersion   int        `json:"version"`
}

type AccountUpdate struct {
	Platform *string `json:"platform"`
	Name     *string `json:"name"`
	URL      *string `json:"url"`
	Note     *string `json:"note"`
}

type Asset struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Note       *string `json:"note"`
	Bucket     *string `json:"bucket"`
	FileName   *string `json:"file_name"`
	PreviewURL string  `json:"preview_url,omitempty"` // presigned GET, filled by the service
}

type AssetUpdate struct {
	Name *string `json:"name"`
	Note *string `json:"note"`
}

// ── Job payload (what the FE posts; names are resolved to ids by the service) ─

type PublishSchedule struct {
	Date string `json:"date"`
	Time string `json:"time"`
	Step any    `json:"step"` // number or string, minutes between clips
}

type BannerConfig struct {
	ID                string                     `json:"id"`
	BannerName        string                     `json:"bannerName"`
	YoutubeAccounts   []string                   `json:"youtubeAccounts"`
	TiktokAccounts    []string                   `json:"tiktokAccounts"`
	InstagramAccounts []string                   `json:"instagramAccounts"`
	Schedules         map[string]PublishSchedule `json:"schedules"`
	Targets           []AccountTarget            `json:"targets"`
}

type VideoCard struct {
	ID                string                     `json:"id"`
	YoutubeURL        string                     `json:"youtubeUrl"`
	MakeVertical      bool                       `json:"makeVertical"`
	AddSubtitles      bool                       `json:"addSubtitles"`
	SliceMode         string                     `json:"sliceMode"` // highlights | full
	MinVideoDuration  int                        `json:"minVideoDuration"`
	AddBackground     bool                       `json:"addBackground"`
	Shape             string                     `json:"shape"`
	BackgroundName    string                     `json:"backgroundName"`
	ApproveMode       string                     `json:"approveMode"` // by_approve | auto_approve
	YoutubeAccounts   []string                   `json:"youtubeAccounts"`
	TiktokAccounts    []string                   `json:"tiktokAccounts"`
	InstagramAccounts []string                   `json:"instagramAccounts"`
	Schedules         map[string]PublishSchedule `json:"schedules"`
	Banners           []BannerConfig             `json:"banners"`
	Targets           []AccountTarget            `json:"targets"`
}

// ── Job views ────────────────────────────────────────────────────────────────

type Publication struct {
	ID          int64      `json:"id"`
	JobID       *string    `json:"job_id"`
	SubJobID    *string    `json:"sub_job_id"`
	ClipID      string     `json:"clip_id"`
	VideoName   *string    `json:"video_name"`
	Name        *string    `json:"name"`
	Description *string    `json:"description"`
	Hashtag     *string    `json:"hashtag"`
	UploadID    *string    `json:"upload_id"`
	PublishDate *time.Time `json:"publish_date"`
	PublishedAt *time.Time `json:"published_at"`
	Status      *string    `json:"status"`
	AccountID   *int64     `json:"account_id"`
	AccountName *string    `json:"account_name"`
	Platform    *string    `json:"platform"`
	PreviewURL  string     `json:"preview_url,omitempty"`
}

type SubJob struct {
	Version         int             `json:"version"`
	SubJobID        string          `json:"sub_job_id"`
	JobID           string          `json:"-"`
	PublishDatetime *time.Time      `json:"publish_datetime"`
	PublishStep     int             `json:"publish_step"`
	Status          string          `json:"status"`
	FailPlace       string          `json:"fail_place"`
	AccountIDs      []int64         `json:"account_ids"`
	AccountNames    []string        `json:"account_names"`
	IsAddBanner     bool            `json:"is_add_banner"`
	BannerID        *int64          `json:"banner_id"`
	BannerName      *string         `json:"banner_name"`
	Outs            []Publication   `json:"outs"`
	Targets         []AccountTarget `json:"targets"`
}

type Job struct {
	JobID            string     `json:"job_id"`
	ChannelID        *int64     `json:"channel_id"`
	YoutubeURL       string     `json:"youtube_url"`
	SourceKind       string     `json:"source_kind"`
	SourceURL        string     `json:"source_url"`
	SourceID         string     `json:"source_id"`
	OutputFormat     string     `json:"output_format"`
	IsVertical       bool       `json:"is_vertical"`
	IsSubtitles      bool       `json:"is_subtitles"`
	SliceMode        string     `json:"slice_mode"`
	IsBackground     bool       `json:"is_background"`
	MinVideoDuration int        `json:"min_video_duration"`
	BackgroundID     *int64     `json:"background_id"`
	BackgroundName   *string    `json:"background_name"`
	IsCircle         bool       `json:"is_circle"`
	ApproveMode      string     `json:"approve_mode"`
	Status           string     `json:"status"`
	FailPlace        string     `json:"fail_place"`
	Trace            *string    `json:"trace"`
	IsArchived       bool       `json:"is_archived"`
	CreateDatetime   *time.Time `json:"create_datetime"`
	LastChange       *time.Time `json:"last_change_datetime"`
	SubJobs          []SubJob   `json:"sub_jobs"`
}

type JobUpdate struct {
	YoutubeURL       *string `json:"youtube_url"`
	IsVertical       *bool   `json:"is_vertical"`
	IsSubtitles      *bool   `json:"is_subtitles"`
	SliceMode        *string `json:"slice_mode"`
	MinVideoDuration *int    `json:"min_video_duration"`
	BackgroundID     *int64  `json:"background_id"`
	IsCircle         *bool   `json:"is_circle"`
	Status           *string `json:"status"`
	FailPlace        *string `json:"fail_place"`
	IsArchived       *bool   `json:"is_archived"`
}

type SubJobUpdate struct {
	PublishDatetime *time.Time `json:"publish_datetime"`
	Status          *string    `json:"status"`
	FailPlace       *string    `json:"fail_place"`
	AccountNames    []string   `json:"account_names"`
	BannerName      *string    `json:"banner_name"`
}

// NewJob is the resolved (ids, not names) form stored by the repository.
type NewJob struct {
	JobID            string
	ChannelID        *int64
	YoutubeURL       string
	IsVertical       bool
	IsSubtitles      bool
	SliceMode        string
	MinVideoDuration int
	BackgroundID     *int64
	IsCircle         bool
	ApproveMode      string
	Trace            string // trace id of the request that created the job (shown in FE, look it up in Tempo)
	SourceKind       string // empty = youtube
	SourceID         string
	OutputFormat     string // empty = legacy
	SubJobs          []NewSubJob
}

type NewSubJob struct {
	SubJobID        string
	BannerID        *int64
	AccountIDs      []int64
	PublishStep     int
	PublishDatetime *time.Time
	Targets         []AccountTarget
}

// ── Clips ────────────────────────────────────────────────────────────────────

type Clip struct {
	ClipID       string    `json:"clip_id"`
	JobID        string    `json:"job_id"`
	BannerObject string    `json:"banner_object"`
	Bucket       string    `json:"bucket"`
	Object       string    `json:"object"`
	Idx          int       `json:"idx"`
	StartSec     float64   `json:"start_sec"`
	EndSec       float64   `json:"end_sec"`
	Title        string    `json:"title"`
	Hashtags     []string  `json:"hashtags"`
	Approval     string    `json:"approval"`
	CreatedAt    time.Time `json:"created_at"`
	PreviewURL   string    `json:"preview_url,omitempty"`
}

// DuePublication is a scheduled publication ready to be sent to the GPU publisher.
type DuePublication struct {
	ID        int64
	ClipID    string
	AccountID int64
	Platform  string
	PublishAt time.Time
}

// ── Channels (monitoring) ────────────────────────────────────────────────────

type Channel struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	URL              string     `json:"url"`
	YTChannelID      string     `json:"yt_channel_id"`
	ApproveMode      string     `json:"approve_mode"`
	Enabled          bool       `json:"enabled"`
	Spec             VideoCard  `json:"spec"` // job template; youtubeUrl is filled per new video
	LastCheckedAt    *time.Time `json:"last_checked_at"`
	FeedErrorCode    string     `json:"feed_error_code,omitempty"`
	LastFeedError    string     `json:"last_feed_error,omitempty"`
	FeedFailureCount int        `json:"feed_failure_count,omitempty"`
	NextFeedCheckAt  *time.Time `json:"next_feed_check_at,omitempty"`
}

type ChannelUpdate struct {
	Name        *string    `json:"name"`
	ApproveMode *string    `json:"approve_mode"`
	Enabled     *bool      `json:"enabled"`
	Spec        *VideoCard `json:"spec"`
}

// FeedVideo is a video found in a channel's feed.
type FeedVideo struct {
	ID    string
	Title string
	URL   string
}
