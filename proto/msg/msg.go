// Package msg holds the RabbitMQ contract shared by main-be and gpu-be.
package msg

import "encoding/json"

// Queue names. gpu.* are consumed by gpu-be, main.* by main-be.
const (
	QueueProcessVideo = "gpu.process_video"
	QueueCandidate    = "gpu.candidate"
	QueueRender       = "gpu.render"
	QueuePublishClip  = "gpu.publish_clip"

	QueueClipsReady    = "main.clips_ready"
	QueueJobStatus     = "main.job_status"
	QueuePublishResult = "main.publish_result"
)

type SliceMode string

const (
	SliceHighlights SliceMode = "highlights"
	SliceFull       SliceMode = "full"
	// SliceWhole republishes the source as one clip: no transcription, the interval is the whole video.
	SliceWhole SliceMode = "whole"
)

type JobOptions struct {
	Vertical     bool      `json:"vertical"`
	Subtitles    bool      `json:"subtitles"`
	SliceMode    SliceMode `json:"slice_mode"`
	MinDuration  int       `json:"min_duration"`
	BackgroundID string    `json:"background_id,omitempty"` // object name in backgrounds bucket
	Shape        string    `json:"shape,omitempty"`         // circle | rectangle
	// BannerIDs are banner object names (banners bucket). One output variant per entry;
	// an empty string means "no banner". Empty list = a single variant without banner.
	BannerIDs []string `json:"banner_ids,omitempty"`
}

type ProcessVideo struct {
	JobID   string     `json:"job_id"`
	YTURL   string     `json:"yt_url"`
	Options JobOptions `json:"options"`
}

type ObjectRef struct {
	Bucket string `json:"bucket"`
	Object string `json:"object"`
}

type Clip struct {
	ClipID         string          `json:"clip_id"`
	JobID          string          `json:"job_id"`
	Object         ObjectRef       `json:"object"`
	Banner         string          `json:"banner,omitempty"` // banner object name this clip was rendered with
	Start          float64         `json:"start"`
	End            float64         `json:"end"`
	Title          string          `json:"title,omitempty"`
	Hashtags       []string        `json:"hashtags,omitempty"`
	SourceManifest json.RawMessage `json:"source_manifest,omitempty"`
}

type ClipsReady struct {
	JobID string `json:"job_id"`
	Clips []Clip `json:"clips"`
}

type JobStatus struct {
	JobID     string `json:"job_id"`
	Stage     string `json:"stage"`  // preproc | candidate | renderer
	Status    string `json:"status"` // queued | running | done | failed
	FailPlace string `json:"fail_place,omitempty"`
}

type PublishClip struct {
	ClipID    string `json:"clip_id"`
	AccountID int64  `json:"account_id"`
	Platform  string `json:"platform"`
	PublishAt string `json:"publish_at"` // RFC3339
}

type PublishResult struct {
	ClipID    string `json:"clip_id"`
	AccountID int64  `json:"account_id"`
	Status    string `json:"status"` // published | failed | scheduled
	UploadID  string `json:"upload_id,omitempty"`
	Error     string `json:"error,omitempty"`
}
