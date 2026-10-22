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
	// Brand switches on the branded layout (see BrandOptions). It replaces Vertical/Subtitles.
	Brand *BrandOptions `json:"brand,omitempty"`
	// Watermark blurs static overlays of the source before any other transform. nil = off.
	Watermark *WatermarkOptions `json:"watermark,omitempty"`
}

// BrandOptions describe the "фирменный стиль" layout: the video is shrunk and pinned to the top centre of
// a 1080x1920 canvas over a background (JobOptions.BackgroundID, empty = blurred copy of the video),
// RU+EN word-by-word subtitles go right under it, the audio pitch is shifted (Content ID) and the banner
// (JobOptions.BannerIDs) sits at the very bottom.
type BrandOptions struct {
	VideoScale     float64 `json:"video_scale"`            // share of the canvas width the video occupies, (0,1]
	PitchSemitones float64 `json:"pitch_semitones"`        // audio pitch shift at the original tempo
	NoSubtitles    bool    `json:"no_subtitles,omitempty"` // skip recognition and burn no subtitles; unset = subtitles on
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
