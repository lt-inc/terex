// Package workflow defines versioned, secret-free messages shared by the scheduler
// and GPU workers. Tokens are stored only by the GPU credential repository.
package workflow

import (
	"terex/proto/msg"
	"time"
)

const (
	QueuePublish = "gpu.workflow.v1.publish"
	QueueRender  = "gpu.workflow.v1.render"
	KindPublish  = "publish"
	KindRender   = "render"
)

type ObjectRef = msg.ObjectRef

// Command is an outbox notification, not an authoritative work snapshot.
type Command struct {
	Kind            string `json:"kind"`
	PublicationID   int64  `json:"publication_id,omitempty"`
	RevisionID      int64  `json:"revision_id,omitempty"`
	AttemptID       string `json:"attempt_id"`
	ExpectedVersion int    `json:"expected_version"`
}

type Cue struct {
	ID    string  `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type Metadata struct {
	Title                  string   `json:"title"`
	Description            string   `json:"description"`
	Hashtags               []string `json:"hashtags"`
	CategoryID             string   `json:"category_id,omitempty"`
	MadeForKids            bool     `json:"made_for_kids"`
	ContainsSyntheticMedia bool     `json:"contains_synthetic_media,omitempty"`
}

type SourceManifest struct {
	Original    ObjectRef      `json:"original"`
	Proxy       ObjectRef      `json:"proxy"`
	Transcript  ObjectRef      `json:"transcript"`
	DurationSec float64        `json:"duration_sec"`
	Options     msg.JobOptions `json:"options"`
	Banner      string         `json:"banner,omitempty"`
	Cues        []Cue          `json:"cues,omitempty"`
	Assembly    *AssemblySpec  `json:"assembly,omitempty"`
}

type PublicationWork struct {
	PublicationID   int64      `json:"publication_id"`
	RevisionID      int64      `json:"revision_id"`
	ClipID          string     `json:"clip_id"`
	AccountID       int64      `json:"account_id"`
	Provider        string     `json:"provider"`
	RemoteAccountID string     `json:"remote_account_id"`
	CredentialRef   ObjectRef  `json:"credential_ref"`
	Media           ObjectRef  `json:"media"`
	Metadata        Metadata   `json:"metadata"`
	Visibility      string     `json:"visibility"`
	State           string     `json:"state"`
	PublishAt       time.Time  `json:"publish_at"`
	Checkpoint      Checkpoint `json:"checkpoint"`
}

type RenderWork struct {
	Epoch      int64          `json:"epoch"`
	Twitch     bool           `json:"twitch,omitempty"`
	RevisionID int64          `json:"revision_id"`
	ClipID     string         `json:"clip_id"`
	JobID      string         `json:"job_id"`
	StartSec   float64        `json:"start_sec"`
	EndSec     float64        `json:"end_sec"`
	Cues       []Cue          `json:"cues"` // absolute times in original source
	Source     SourceManifest `json:"source"`
	Output     ObjectRef      `json:"output"`
}

type ClaimRequest struct {
	Command
	WorkerID string `json:"worker_id"`
}
type Lease struct {
	Granted         bool             `json:"granted"`
	AttemptID       string           `json:"attempt_id"`
	LeaseToken      string           `json:"lease_token"`
	ExpiresAt       time.Time        `json:"expires_at"`
	ExpectedVersion int              `json:"expected_version"`
	Publication     *PublicationWork `json:"publication,omitempty"`
	Render          *RenderWork      `json:"render,omitempty"`
}
type LeaseRequest struct {
	AttemptID       string `json:"attempt_id"`
	LeaseToken      string `json:"lease_token"`
	ExpectedVersion int    `json:"expected_version"`
}
type Permit struct {
	Allowed    bool      `json:"allowed"`
	CanPublish bool      `json:"can_publish"`
	ExpiresAt  time.Time `json:"expires_at"`
	State      string    `json:"state,omitempty"`
}

// SessionURI is persisted before bytes are sent. FinalRequestStarted records the
// ambiguity window: an expired session must never cause a duplicate insert.
type Checkpoint struct {
	State                 string     `json:"state"`
	SessionURI            string     `json:"session_uri,omitempty"`
	ConfirmedBytes        int64      `json:"confirmed_bytes"`
	TotalBytes            int64      `json:"total_bytes"`
	RemoteID              string     `json:"remote_id,omitempty"`
	RemoteURL             string     `json:"remote_url,omitempty"`
	FinalRequestStarted   bool       `json:"final_request_started"`
	UploadCompletedAt     *time.Time `json:"upload_completed_at,omitempty"`
	ProcessingCompletedAt *time.Time `json:"processing_completed_at,omitempty"`
	VerifiedAt            *time.Time `json:"verified_at,omitempty"`
}
type CheckpointRequest struct {
	LeaseRequest
	Checkpoint
}
type CompleteRequest struct {
	LeaseRequest
	Checkpoint
	Media  ObjectRef       `json:"media,omitempty"`
	Source *SourceManifest `json:"source,omitempty"`
}
type FailRequest struct {
	LeaseRequest
	State             string `json:"state"` // failed | blocked | needs_attention | cancelled
	Code              string `json:"code"`
	Message           string `json:"message"`
	Retryable         bool   `json:"retryable"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
	ReconnectRequired bool   `json:"reconnect_required"`
}
type Result struct {
	Accepted bool `json:"accepted"`
}

type OAuthURLRequest struct {
	AccountID   int64  `json:"account_id"`
	State       string `json:"state"`
	RedirectURL string `json:"redirect_url"`
}
type OAuthURLResponse struct {
	URL string `json:"url"`
}
type OAuthExchangeRequest struct {
	State            string    `json:"state"`
	AccountID        int64     `json:"account_id"`
	Code             string    `json:"code"`
	RedirectURL      string    `json:"redirect_url"`
	ExpectedRemoteID string    `json:"expected_remote_id,omitempty"`
	CredentialRef    ObjectRef `json:"credential_ref,omitempty"`
}
type Connection struct {
	Provider      string     `json:"provider"`
	RemoteID      string     `json:"remote_id"`
	DisplayName   string     `json:"display_name"`
	CredentialRef ObjectRef  `json:"credential_ref"`
	Scopes        []string   `json:"scopes"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	Status        string     `json:"status"`
	Error         string     `json:"error,omitempty"`
}
type ConnectionRequest struct {
	AccountID        int64     `json:"account_id"`
	CredentialRef    ObjectRef `json:"credential_ref"`
	ExpectedRemoteID string    `json:"expected_remote_id,omitempty"`
}

// ChannelStats statuses. Anything but StatsOK carries a message in Error and no data.
const (
	StatsOK          = "ok"
	StatsNeedsReauth = "needs_reauth"
	StatsQuota       = "quota"
	StatsError       = "error"
)

type ChannelStatsRequest struct {
	AccountID        int64     `json:"account_id"`
	CredentialRef    ObjectRef `json:"credential_ref"`
	ExpectedRemoteID string    `json:"expected_remote_id,omitempty"`
	MaxVideos        int       `json:"max_videos"`
}
type ChannelStats struct {
	ChannelID    string `json:"channel_id"`
	Title        string `json:"title"`
	CustomURL    string `json:"custom_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Subscribers  *int64 `json:"subscribers"` // nil when the owner hides the count
	Views        int64  `json:"views"`
	Videos       int64  `json:"videos"`
}
type VideoStats struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	ThumbnailURL string    `json:"thumbnail_url,omitempty"`
	PublishedAt  time.Time `json:"published_at"`
	DurationSec  int       `json:"duration_sec"`
	Views        int64     `json:"views"`
	Likes        *int64    `json:"likes"`    // nil when hidden
	Comments     *int64    `json:"comments"` // nil when disabled
}
type ChannelStatsResponse struct {
	Status     string       `json:"status"`
	Error      string       `json:"error,omitempty"`
	Channel    ChannelStats `json:"channel"`
	Videos     []VideoStats `json:"videos"`
	QuotaUnits int          `json:"quota_units"` // YouTube Data API units spent by this call
}

type EditorContextRequest struct {
	JobID    string         `json:"job_id"`
	ClipID   string         `json:"clip_id"`
	StartSec float64        `json:"start_sec"`
	EndSec   float64        `json:"end_sec"`
	Source   SourceManifest `json:"source"`
}
type EditorContextResponse struct {
	Source          SourceManifest `json:"source"`
	SourceURL       string         `json:"source_url"`
	SourceAvailable bool           `json:"source_available"`
	Cues            []Cue          `json:"cues"`
}
