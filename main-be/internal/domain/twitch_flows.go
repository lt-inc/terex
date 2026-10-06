package domain

import (
	"context"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

type FlowClipQuery struct {
	Limit, Offset                   int
	State, Format, BroadcasterID, Q string
	CollectorSourceIDs              []string
}
type FlowClipReason struct {
	Code          string     `json:"code"`
	Message       string     `json:"message"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	Actions       []string   `json:"actions"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	Attempt       int        `json:"attempt,omitempty"`
}
type FlowClipOutput struct {
	ClipID           string     `json:"clip_id"`
	RevisionID       int64      `json:"revision_id"`
	Format           string     `json:"format"`
	RenderState      string     `json:"render_state"`
	Approval         string     `json:"approval"`
	PublicationState string     `json:"publication_state"`
	PublishAt        *time.Time `json:"publish_at,omitempty"`
	Error            string     `json:"error,omitempty"`
}
type FlowClip struct {
	Source       twitch.Source    `json:"source"`
	SourceState  string           `json:"source_state"`
	Reason       FlowClipReason   `json:"reason"`
	ImportID     string           `json:"import_id"`
	SourceClipID string           `json:"source_clip_id"`
	MediaID      string           `json:"media_id,omitempty"`
	Excluded     bool             `json:"excluded"`
	Rejected     bool             `json:"rejected"`
	Outputs      []FlowClipOutput `json:"outputs"`
}
type FlowClipPage struct {
	Items  []FlowClip `json:"items"`
	Total  int        `json:"total"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
}
type Preview struct {
	URL        string    `json:"url"`
	ExpiresAt  time.Time `json:"expires_at"`
	ClipID     string    `json:"clip_id"`
	RevisionID int64     `json:"revision_id"`
	Kind       string    `json:"kind"`
}

// Kept separate from the v1 repository contract for existing integrations.
type TwitchFlowRepo interface {
	ListTwitchFlows(context.Context) ([]twitch.Flow, error)
	GetTwitchFlow(context.Context, string) (twitch.Flow, error)
	SaveTwitchFlow(context.Context, twitch.Flow, bool, time.Time) (twitch.Flow, error)
	DeleteTwitchFlow(context.Context, string, time.Time) error
	ObserveTwitchSources(context.Context, twitch.ObservationRequest, time.Time) ([]twitch.Import, error)
	ReserveFlowImport(context.Context, twitch.ImportRequest, time.Time) (twitch.Import, error)
	GetFlowImport(context.Context, string, string) (twitch.Import, error)
	ListFlowImports(context.Context, string) ([]twitch.Import, error)
	SetFlowUpload(context.Context, string, int64, workflow.ObjectRef, time.Time) error
	FinishFlowImport(context.Context, string, int64, *workflow.VerifiedMedia, time.Time) (twitch.Import, error)
	ListFlowClips(context.Context, string, FlowClipQuery) (FlowClipPage, error)
	ActFlowSource(context.Context, string, string, string, bool, time.Time, ...int64) error
	AutoFlowCompilation(context.Context, string, time.Time) (AutoCompilationResult, error)
	TickTwitchFlows(context.Context, time.Time) error
	TwitchSourcePreview(context.Context, string, string) (workflow.ObjectRef, error)
}
