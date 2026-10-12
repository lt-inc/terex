package domain

import (
	"context"
	"errors"
	"time"

	"terex/proto/msg"
)

var ErrNotFound = errors.New("not found")

// AccountRepo, AssetRepo, JobRepo, ClipRepo and ChannelRepo are implemented by repository/postgres.

type AccountRepo interface {
	ListAccounts(ctx context.Context) ([]Account, error)
	GetAccount(ctx context.Context, id int64) (Account, error)
	GetOrCreateAccount(ctx context.Context, platform, name string) (int64, error)
	CreateAccount(ctx context.Context, a Account) (Account, error)
	UpdateAccount(ctx context.Context, id int64, u AccountUpdate, bucket, file *string) (Account, error)
	DeleteAccount(ctx context.Context, id int64) error
	AccountNamesByPlatform(ctx context.Context) (map[string][]string, error)
}

type AssetRepo interface {
	ListAssets(ctx context.Context, kind AssetKind) ([]Asset, error)
	GetAsset(ctx context.Context, kind AssetKind, id int64) (Asset, error)
	GetOrCreateAssetByName(ctx context.Context, kind AssetKind, name string) (int64, error)
	CreateAsset(ctx context.Context, kind AssetKind, a Asset) (Asset, error)
	UpdateAsset(ctx context.Context, kind AssetKind, id int64, u AssetUpdate) (Asset, error)
	DeleteAsset(ctx context.Context, kind AssetKind, id int64) error
}

type JobRepo interface {
	CreateJob(ctx context.Context, j NewJob) error
	ListJobs(ctx context.Context, showArchived bool) ([]Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	UpdateJob(ctx context.Context, id string, u JobUpdate) error
	DeleteJob(ctx context.Context, id string) error
	UpdateSubJob(ctx context.Context, id string, u SubJobUpdate, accountIDs []int64, bannerID *int64) error
	DeleteSubJob(ctx context.Context, id string) error
	SetJobStatus(ctx context.Context, id, status, failPlace string) error
	// JobProcessMessage builds the GPU command for a stored job (banner variants resolved to object names).
	JobProcessMessage(ctx context.Context, id string) (msg.ProcessVideo, error)
}

type ClipRepo interface {
	SaveClips(ctx context.Context, jobID string, clips []Clip) error
	ListClips(ctx context.Context, approval string) ([]Clip, error)
	GetClip(ctx context.Context, id string) (Clip, error)
	SetApproval(ctx context.Context, id, approval string) error
	// SchedulePublications inserts publications for an approved clip (idempotent).
	SchedulePublications(ctx context.Context, clipID string, now time.Time) (int, error)
	DuePublications(ctx context.Context, now time.Time, limit int) ([]DuePublication, error)
	MarkPublication(ctx context.Context, clipID string, accountID int64, status, uploadID, errMsg string) error
	AccountPublications(ctx context.Context, accountID int64, kind string) ([]Publication, error) // kind: clips|scheduled|published
	JobApproveMode(ctx context.Context, jobID string) (string, error)
}

type ChannelRepo interface {
	ListChannels(ctx context.Context) ([]Channel, error)
	GetChannel(ctx context.Context, id int64) (Channel, error)
	CreateChannel(ctx context.Context, c Channel) (Channel, error)
	UpdateChannel(ctx context.Context, id int64, u ChannelUpdate) (Channel, error)
	DeleteChannel(ctx context.Context, id int64) error
	// MarkSeen records video ids; returns the ones not seen before.
	MarkSeen(ctx context.Context, channelID int64, ids []string) ([]string, error)
	TouchChannel(ctx context.Context, id int64, at time.Time) error
	RecordChannelFeedFailure(ctx context.Context, id int64, code, message string, nextCheck time.Time) error
}

// Broker publishes to RabbitMQ (implemented by proto/amqpx).
type Broker interface {
	Publish(ctx context.Context, queue string, m any) error
}

// Gpu is the gRPC client to gpu-be: the only way main-be touches files.
type Gpu interface {
	PresignGet(ctx context.Context, bucket, object string) (string, error)
	PresignPut(ctx context.Context, kind AssetKind, fileName string) (url, bucket, object string, err error)
	PutAuthFile(ctx context.Context, platform, account string, content []byte) (bucket, object string, err error)
	DeleteObject(ctx context.Context, bucket, object string) error
	Health(ctx context.Context) error
}

// Feed reads a channel's public feed (YouTube RSS).
type Feed interface {
	ResolveChannel(ctx context.Context, url string) (channelID string, err error)
	Latest(ctx context.Context, channelID string) ([]FeedVideo, error)
}
