package domain

import (
	"context"
	"io"
	"time"
)

// Broker publishes messages to a queue (implemented in repository/broker).
type Broker interface {
	Publish(ctx context.Context, queue string, msg any) error
}

// ObjectStore is the MinIO abstraction (implemented in repository/minio).
type ObjectStore interface {
	Upload(ctx context.Context, ref ObjectRef, path string) error
	Download(ctx context.Context, ref ObjectRef, path string) error
	Delete(ctx context.Context, ref ObjectRef) error
	Put(ctx context.Context, ref ObjectRef, r io.Reader, size int64) error
	PresignGet(ctx context.Context, ref ObjectRef, ttl time.Duration) (string, error)
	PresignPut(ctx context.Context, ref ObjectRef, ttl time.Duration) (string, error)
}

// Downloader wraps yt-dlp.
type Downloader interface {
	Download(ctx context.Context, url, dest string) error
}

// VerticalSpec describes the 1080x1920 conversion.
type VerticalSpec struct {
	Width  int
	Blur   int
	Circle bool
	BgPath string // optional looping background video
}

// Media wraps ffmpeg.
type Media interface {
	VideoProxy(ctx context.Context, in, out string, height int) error
	AudioProxy(ctx context.Context, in, out string) error
	Vertical(ctx context.Context, in, out string, v VerticalSpec) error
	BurnSubtitles(ctx context.Context, in, ass, out string) error
	Banner(ctx context.Context, in, bannerPath, out string, rectMode bool) error
	Cut(ctx context.Context, in, out string, start, end float64) error
}

type Word struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

type Segment struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
	Words []Word  `json:"words,omitempty"`
}

// Transcriber wraps whisper and returns word-level segments.
type Transcriber interface {
	Transcribe(ctx context.Context, audioPath string) ([]Segment, error)
}

type Interval struct {
	ID    int      `json:"id"`
	Start float64  `json:"start"`
	End   float64  `json:"end"`
	Text  string   `json:"text,omitempty"`
	Title string   `json:"title,omitempty"`
	Tags  []string `json:"hashtags,omitempty"`
}

// Sentence is the unit the LLM picks clips from: whole sentences guarantee that a cut never lands
// in the middle of a word or a phrase. Start/End come from the first/last word timestamps.
type Sentence struct {
	ID    int      `json:"id"`
	Start float64  `json:"start"`
	End   float64  `json:"end"`
	Text  string   `json:"text"`
	Words int      `json:"words"`
	Tags  []string `json:"tags,omitempty"` // energy:high, laugh, fast, pause_before (relative to this video)
}

// Pick is a clip chosen by the LLM as a run of consecutive sentences [From..To] (inclusive).
type Pick struct {
	From     int
	To       int
	Title    string
	Hashtags []string
}

// ClipLimits bound the clip length the LLM may choose, and how many clips it may return.
type ClipLimits struct {
	MinSec   float64
	MaxSec   float64
	MaxClips int
}

// HighlightFinder asks an LLM to pick clips from annotated sentences (OpenRouter).
type HighlightFinder interface {
	Find(ctx context.Context, sents []Sentence, lim ClipLimits) ([]Pick, error)
}

// WordTranslation is one spoken word rendered in both subtitle languages. An empty side means the word
// has no counterpart of its own (it is part of a neighbour's phrase); the previous text stays on screen.
type WordTranslation struct{ RU, EN string }

// WordTranslator translates a spoken word sequence word by word (OpenRouter); the result has one entry per word.
type WordTranslator interface {
	TranslateWords(ctx context.Context, words []string) ([]WordTranslation, error)
}

// EnergySeries is loudness (dBFS) in consecutive windows of Step seconds.
type EnergySeries struct {
	Step float64
	DB   []float64
}

// AudioEvent is the YAMNet probability of event groups for [Start, End) seconds.
type AudioEvent struct {
	Start   float64
	End     float64
	Laugh   float64 // laughter, applause, cheering
	Shout   float64 // shouting, yelling, screaming
	Gasp    float64
	Music   float64 // music or singing
	Silence float64
}

// AudioAnalyzer extracts acoustic hints from the audio proxy.
type AudioAnalyzer interface {
	Energy(ctx context.Context, audioPath string) (EnergySeries, error)
	Events(ctx context.Context, audioPath string) ([]AudioEvent, error)
}

// JobState stores per-job stage status and clips so gRPC can answer sync queries.
type JobState interface {
	SetStatus(ctx context.Context, s JobStatus) error
	GetStatus(ctx context.Context, jobID string) (JobStatus, time.Time, error)
	SaveClips(ctx context.Context, jobID string, clips []Clip) error
	ListClips(ctx context.Context, jobID string) ([]Clip, error)
}
