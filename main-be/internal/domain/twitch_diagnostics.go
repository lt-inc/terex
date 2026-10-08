package domain

import (
	"context"
	"terex/proto/twitch"
	"time"
)

type TwitchDiagnosticBudgetFlow struct {
	FlowID string `json:"flow_id"`
	Name   string `json:"name"`
	Format string `json:"format"`
	Spent  int    `json:"spent"`
}
type TwitchDiagnosticCollectorClip struct {
	ClipID string `json:"clip_id"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}
type TwitchDiagnosticCollector struct {
	Available                bool                            `json:"available"`
	Message                  string                          `json:"message,omitempty"`
	Spent                    int                             `json:"spent"`
	Remaining                int                             `json:"remaining"`
	DailyLimit               int                             `json:"daily_limit"`
	Timezone                 string                          `json:"timezone"`
	CompilationBudgetPercent int                             `json:"compilation_budget_percent"`
	AdmissionMode            string                          `json:"admission_mode"`
	ByFlow                   []TwitchDiagnosticBudgetFlow    `json:"by_flow"`
	Clips                    []TwitchDiagnosticCollectorClip `json:"clips,omitempty"`
	Discovery                []TwitchDiagnosticDiscovery     `json:"discovery"`
}
type TwitchDiagnosticDiscovery struct {
	FlowID           string    `json:"flow_id"`
	BroadcasterID    string    `json:"broadcaster_id,omitempty"`
	Language         string    `json:"language,omitempty"`
	Fetched          int       `json:"fetched"`
	LanguageMatched  int       `json:"language_matched"`
	LanguageRejected int       `json:"language_rejected"`
	LanguageUnknown  int       `json:"language_unknown"`
	Pages            int       `json:"pages"`
	PageLimitReached bool      `json:"page_limit_reached"`
	CheckedAt        time.Time `json:"checked_at"`
	Error            string    `json:"error,omitempty"`
}
type TwitchDiagnosticCompetitor struct {
	FlowID    string     `json:"flow_id"`
	Name      string     `json:"name"`
	Format    string     `json:"format"`
	Scheduled int        `json:"scheduled"`
	Preparing int        `json:"preparing"`
	NearestAt *time.Time `json:"nearest_at,omitempty"`
}
type TwitchFlowDiagnostics struct {
	FlowID                   string                       `json:"flow_id"`
	GeneratedAt              time.Time                    `json:"generated_at"`
	Compilation              AutoCompilationResult        `json:"compilation"`
	Capacity                 twitch.FlowCapacity          `json:"capacity"`
	Sources                  map[string]int               `json:"sources"`
	Competitors              []TwitchDiagnosticCompetitor `json:"competitors"`
	Collector                TwitchDiagnosticCollector    `json:"collector"`
	AverageDurationSec       float64                      `json:"average_duration_sec"`
	AverageEstimated         bool                         `json:"average_estimated"`
	EstimatedAdditionalClips int                          `json:"estimated_additional_clips"`
	ExpectedClips            int                          `json:"expected_clips"`
	ExpectedDurationSec      float64                      `json:"expected_duration_sec"`
	Feasibility              string                       `json:"feasibility"`
	Messages                 []string                     `json:"messages"`
	PendingClipIDs           []string                     `json:"-"`
	PendingStates            map[string]string            `json:"-"`
}
type TwitchDiagnosticsRepo interface {
	TwitchFlowDiagnostics(context.Context, string, time.Time) (TwitchFlowDiagnostics, error)
}
