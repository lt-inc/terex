package domain

import (
	"context"
	"encoding/json"
	"errors"
	"terex/proto/workflow"
	"time"
)

var ErrConflict = errors.New("state or version changed; reload and retry")
var ErrInvalid = errors.New("invalid input")

type Cue struct {
	ID    string  `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type EditSpec struct {
	StartSec         float64  `json:"start_sec"`
	EndSec           float64  `json:"end_sec"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Visibility       string   `json:"visibility"`
	Hashtags         []string `json:"hashtags"`
	Cues             []Cue    `json:"cues"`
	SubtitlesEnabled bool     `json:"subtitles_enabled"`
}

type Revision struct {
	ID      int64  `json:"revision_id"`
	ClipID  string `json:"clip_id"`
	Version int    `json:"version"`
	EditSpec
	RenderState string          `json:"render_state"`
	Bucket      string          `json:"-"`
	Object      string          `json:"-"`
	Manifest    json.RawMessage `json:"-"`
	ApprovedAt  *time.Time      `json:"approved_at"`
	Error       *string         `json:"error"`
	CreatedAt   time.Time       `json:"created_at"`
}

type Editor struct {
	Revision
	ApprovedRevisionID *int64            `json:"approved_revision_id"`
	SourceURL          string            `json:"source_url"`
	SourceDurationSec  float64           `json:"source_duration_sec"`
	SourceAvailable    bool              `json:"source_available"`
	SourceError        string            `json:"source_error,omitempty"`
	RenderedPreviewURL string            `json:"rendered_preview_url"`
	Revisions          []RevisionSummary `json:"revisions"`
}
type RevisionSummary struct {
	ID          int64      `json:"revision_id"`
	Version     int        `json:"version"`
	Title       string     `json:"title"`
	RenderState string     `json:"render_state"`
	ApprovedAt  *time.Time `json:"approved_at"`
	CreatedAt   time.Time  `json:"created_at"`
	Error       *string    `json:"error"`
}

type EditRequest struct {
	Version int `json:"expected_version"`
	EditSpec
}

type AccountTarget struct {
	AccountID       int64      `json:"account_id"`
	StartAt         *time.Time `json:"start_at"`
	IntervalSeconds int        `json:"interval_seconds"`
	Timezone        string     `json:"timezone"`
	LegacyShared    bool       `json:"legacy_shared,omitempty"`
}
type TargetsResponse struct {
	Version int             `json:"version"`
	Targets []AccountTarget `json:"targets"`
}
type TargetsRequest struct {
	Version int             `json:"expected_version"`
	Targets []AccountTarget `json:"targets"`
}

type PublicationMetadata struct {
	Visibility  string   `json:"visibility"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Hashtags    []string `json:"hashtags"`
	CategoryID  string   `json:"category_id,omitempty"`
	MadeForKids bool     `json:"made_for_kids"`
}

type PublicationRecord struct {
	ID          int64     `json:"id"`
	ClipID      string    `json:"clip_id"`
	RevisionID  int64     `json:"revision_id"`
	AccountID   int64     `json:"account_id"`
	AccountName string    `json:"account_name"`
	Platform    string    `json:"platform"`
	PublishAt   time.Time `json:"publish_at"`
	Timezone    string    `json:"timezone"`
	Status      string    `json:"status"`
	State       string    `json:"state"`
	Version     int       `json:"version"`
	PublicationMetadata
	CancelRequested bool       `json:"cancel_requested"`
	UploadID        *string    `json:"upload_id"`
	ExternalURL     *string    `json:"external_url"`
	Error           *string    `json:"error"`
	UploadedAt      *time.Time `json:"uploaded_at"`
	ProcessedAt     *time.Time `json:"processed_at"`
	PublishedAt     *time.Time `json:"published_at"`
	LastVerifiedAt  *time.Time `json:"last_verified_at"`
	StartedAt       *time.Time `json:"started_at"`
	PreviewURL      string     `json:"preview_url,omitempty"`
}

type PublicationFilter struct {
	From, To        *time.Time
	AccountID       int64
	Platform, State string
	Limit, Offset   int
}
type PublicationPatch struct {
	Version     int        `json:"expected_version"`
	PublishAt   *time.Time `json:"publish_at"`
	Timezone    *string    `json:"timezone"`
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Hashtags    *[]string  `json:"hashtags"`
}
type PublicationAction struct {
	Version int `json:"expected_version"`
}
type PublicationEvent struct {
	AttemptID *string         `json:"attempt_id,omitempty"`
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	State     string          `json:"state"`
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}
type OutboxRecord struct {
	ID, Kind string
	Payload  json.RawMessage
}

type RevisionRepo interface {
	LatestRevision(context.Context, string) (Revision, error)
	InitializeRevision(context.Context, string) error
	SaveRevision(context.Context, string, EditRequest, json.RawMessage, bool) (Revision, error)
	ApproveRevision(context.Context, string, time.Time) error
	ApproveRevisionWithVersion(context.Context, string, int, time.Time) error
	RejectRevision(context.Context, string) error
	FinishRevision(context.Context, int64, string, string, string) error
	RevisionByID(context.Context, int64) (Revision, error)
	SetRevisionManifest(context.Context, int64, json.RawMessage) error
	RevisionHistory(context.Context, string) ([]Revision, error)
	RestoreRevision(context.Context, string, int, int64) error
	RetryRevision(context.Context, string, int) error
}

type WorkflowRepo interface {
	RevisionRepo
	Targets(context.Context, string) ([]AccountTarget, error)
	ReplaceTargets(context.Context, string, int, []AccountTarget) error
	TargetVersion(context.Context, string) (int, error)
	ListPublicationRecords(context.Context, PublicationFilter) ([]PublicationRecord, error)
	GetPublicationRecord(context.Context, int64) (PublicationRecord, error)
	PatchPublication(context.Context, int64, PublicationPatch) error
	ActPublication(context.Context, int64, string, int, time.Time) error
	PublicationEvents(context.Context, int64) ([]PublicationEvent, error)
	PreparePublications(context.Context, time.Time, int) error
	PendingOutbox(context.Context, time.Time, int) ([]OutboxRecord, error)
	MarkOutbox(context.Context, string, error) error
	PutOAuthState(context.Context, string, int64, string, time.Time) error
	ConsumeOAuthState(context.Context, string, time.Time) (int64, string, error)
	SetAccountConnection(context.Context, int64, string, string, json.RawMessage, []string, *time.Time, string) error
	CredentialRef(context.Context, int64) (json.RawMessage, error)
	ConfirmAccountConnection(context.Context, int64, int, string) error
}

type CoordinatorRepo interface {
	ClaimWorkflow(context.Context, *workflow.ClaimRequest, time.Time) (*workflow.Lease, error)
	RenewWorkflow(context.Context, *workflow.LeaseRequest, time.Time) (*workflow.Permit, error)
	CheckpointWorkflow(context.Context, *workflow.CheckpointRequest, time.Time) (bool, error)
	CompleteWorkflow(context.Context, *workflow.CompleteRequest, time.Time) (bool, error)
	FailWorkflow(context.Context, *workflow.FailRequest, time.Time) (bool, error)
}
