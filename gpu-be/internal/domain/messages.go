package domain

import "terex/proto/msg"

// Queue names (RabbitMQ). Modules communicate only through these.
const (
	QueueProcessVideo  = msg.QueueProcessVideo
	QueueCandidate     = msg.QueueCandidate
	QueueRender        = msg.QueueRender
	QueuePublishClip   = msg.QueuePublishClip
	QueueClipsReady    = msg.QueueClipsReady
	QueueJobStatus     = msg.QueueJobStatus
	QueuePublishResult = msg.QueuePublishResult
)

// Buckets in MinIO.
const (
	BucketDownloads   = "downloads"
	BucketProxy       = "proxy"
	BucketTranscripts = "transcripts"
	BucketClips       = "clips"
	BucketBanners     = "banners"
	BucketBackgrounds = "backgrounds"
	BucketCookies     = "cookies"
)

type (
	SliceMode     = msg.SliceMode
	JobOptions    = msg.JobOptions
	ProcessVideo  = msg.ProcessVideo
	ObjectRef     = msg.ObjectRef
	Clip          = msg.Clip
	ClipsReady    = msg.ClipsReady
	JobStatus     = msg.JobStatus
	PublishClip   = msg.PublishClip
	PublishResult = msg.PublishResult
)

const (
	SliceHighlights = msg.SliceHighlights
	SliceFull       = msg.SliceFull
)
