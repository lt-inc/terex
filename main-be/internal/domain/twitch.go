package domain

import (
	"context"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

type Compilation struct {
	ClipID     string                          `json:"clip_id"`
	RevisionID int64                           `json:"revision_id"`
	Version    int                             `json:"version"`
	Title      string                          `json:"title"`
	State      string                          `json:"state"`
	Error      string                          `json:"error,omitempty"`
	ApprovedAt *time.Time                      `json:"approved_at"`
	PreviewURL string                          `json:"preview_url,omitempty"`
	Items      []twitch.CompilationItemRequest `json:"items"`
	CreatedAt  time.Time                       `json:"created_at"`
	Bucket     string                          `json:"-"`
	Object     string                          `json:"-"`
}
type CompilationUpdate struct {
	ExpectedVersion int `json:"expected_version"`
	twitch.CompilationRequest
}
type TwitchRepo interface {
	TwitchSettings(context.Context) (twitch.Settings, error)
	SaveTwitchSettings(context.Context, twitch.Settings) error
	ReserveTwitchImport(context.Context, twitch.ImportRequest, time.Time) (twitch.Import, error)
	GetTwitchImport(context.Context, string) (twitch.Import, error)
	ListTwitchImports(context.Context, bool, string) ([]twitch.Import, error)
	SetTwitchUpload(context.Context, string, workflow.ObjectRef, time.Time) error
	FailTwitchImport(context.Context, string, string) error
	FinishTwitchImport(context.Context, string, *workflow.VerifiedMedia, time.Time) (twitch.Import, error)
	RejectTwitchImport(context.Context, string, bool) error
	CreateCompilation(context.Context, twitch.CompilationRequest, *time.Time, time.Time) (Compilation, error)
	GetCompilation(context.Context, string) (Compilation, error)
	ListCompilations(context.Context) ([]Compilation, error)
	UpdateCompilation(context.Context, string, CompilationUpdate, time.Time) (Compilation, error)
	AutoCompilation(context.Context, time.Time) (*Compilation, error)
}
