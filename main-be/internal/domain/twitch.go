package domain

import (
	"context"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

type Compilation struct {
	FlowID     string                          `json:"flow_id,omitempty"`
	FlowConfig *twitch.Flow                    `json:"flow_config,omitempty"`
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

// AutoCompilationResult preserves the HTTP created/compilation fields while
// explaining the first unmet requirement. Sources is absent until evaluated.
type AutoCompilationResult struct {
	Created            bool                     `json:"created"`
	Compilation        *Compilation             `json:"compilation"`
	Reason             string                   `json:"reason"`
	Message            string                   `json:"message"`
	LocalDate          string                   `json:"local_date"`
	Timezone           string                   `json:"timezone"`
	ScheduledAt        time.Time                `json:"scheduled_at"`
	NextAttemptAt      *time.Time               `json:"next_attempt_at,omitempty"`
	MinimumClips       int                      `json:"minimum_clips"`
	WindowHours        int                      `json:"window_hours"`
	MinimumDurationSec float64                  `json:"minimum_duration_sec"`
	ReadyDurationSec   float64                  `json:"ready_duration_sec"`
	MissingDurationSec float64                  `json:"missing_duration_sec"`
	Sources            *CompilationSourceCounts `json:"sources,omitempty"`
}

// Each import is counted once, in rejection, readiness, age, reuse order.
type CompilationSourceCounts struct {
	Total         int `json:"total"`
	Eligible      int `json:"eligible"`
	Rejected      int `json:"rejected"`
	NotReady      int `json:"not_ready"`
	OutsideWindow int `json:"outside_window"`
	AlreadyUsed   int `json:"already_used"`
	Filtered      int `json:"filtered"`
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
	AutoCompilation(context.Context, time.Time) (AutoCompilationResult, error)
}
