package domain

import (
	"context"
	"errors"
	"time"
)

const BucketCredentials = "credentials"

var ErrReauthentication = errors.New("Google account must be connected again")
var ErrUploadUncertain = errors.New("upload outcome is unknown; manual reconciliation required")
var ErrUnsupported = errors.New("provider operation is not supported")

// Capabilities describe real platform operations, rather than promising the same
// scheduling and cancellation semantics for every social network.
type PublishingCapabilities struct {
	ResumableUpload    bool `json:"resumable_upload"`
	PullFromURL        bool `json:"pull_from_url"`
	DeferredVisibility bool `json:"deferred_visibility"`
	NativeSchedule     bool `json:"native_schedule"`
	CancelTransfer     bool `json:"cancel_transfer"`
}

type PublicationMetadata struct {
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	Tags                   []string `json:"tags,omitempty"`
	CategoryID             string   `json:"category_id,omitempty"`
	MadeForKids            bool     `json:"made_for_kids"`
	ContainsSyntheticMedia bool     `json:"contains_synthetic_media"`
}

type UploadSession struct {
	URI                 string `json:"uri,omitempty"`
	Offset              int64  `json:"offset"`
	Size                int64  `json:"size"`
	VideoID             string `json:"video_id,omitempty"`
	FinalRequestStarted bool   `json:"final_request_started"`
}

type RemoteVideo struct {
	ID               string `json:"id"`
	Privacy          string `json:"privacy"`
	UploadStatus     string `json:"upload_status"`
	ProcessingStatus string `json:"processing_status"`
	Failure          string `json:"failure,omitempty"`
}

type PublishingProvider interface {
	Capabilities() PublishingCapabilities
	Validate(PublicationMetadata) error
	Prepare(context.Context, PublicationMetadata, int64) (UploadSession, error)
	// Transfer checkpoints every acknowledged chunk. A caller must persist Prepare
	// before invoking Transfer, and persist the video id before changing visibility.
	Transfer(context.Context, string, UploadSession, func(UploadSession) error) (UploadSession, error)
	Inspect(context.Context, string) (RemoteVideo, error)
}

type PublishFinalizer interface {
	MakePublic(context.Context, string) error
}
type VisibilityFinalizer interface {
	SetVisibility(context.Context, string, string) error
}
type URLUploadPreparer interface {
	PrepareURL(context.Context, PublicationMetadata, string) (UploadSession, error)
}
type MetadataUpdater interface {
	UpdateMetadata(context.Context, string, PublicationMetadata) error
}
type UploadCanceller interface {
	Cancel(context.Context, UploadSession) error
}
type NativeScheduler interface {
	Schedule(context.Context, string, time.Time) error
}

type GoogleCredential struct {
	AccountID    string    `json:"account_id"`
	ChannelID    string    `json:"channel_id"`
	ChannelTitle string    `json:"channel_title"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       string    `json:"scopes"`
}

type CredentialRepository interface {
	Get(context.Context, string) (GoogleCredential, error)
	Put(context.Context, GoogleCredential) error
	Delete(context.Context, string) error
}
