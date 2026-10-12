package domain

import (
	"context"
	"encoding/json"
	"terex/proto/twitch"
	"time"
)

type TwitchMonitoringRepo interface {
	TwitchRuntimeWork(context.Context, time.Time) (twitch.RuntimeWork, error)
	SaveTwitchMonitoring(context.Context, string, string, int64, time.Time, any) error
	CleanupTwitchMonitoring(context.Context, int64) error
}

type TwitchCollectorStatus struct {
	Available            bool                `json:"available"`
	Running              bool                `json:"running"`
	Paused               bool                `json:"paused"`
	CredentialsReady     bool                `json:"credentials_ready"`
	ConfigAvailable      bool                `json:"config_available"`
	ConfigObservedAt     *time.Time          `json:"config_observed_at,omitempty"`
	LastCheckAt          *time.Time          `json:"last_check_at,omitempty"`
	LastError            string              `json:"last_error,omitempty"`
	BlockReason          string              `json:"block_reason,omitempty"`
	Channels             int                 `json:"channels"`
	Queued               int                 `json:"queued"`
	WaitingAssignment    int                 `json:"waiting_assignment"`
	VerificationPending  int                 `json:"verification_pending"`
	Verifying            int                 `json:"verifying"`
	OldestAuthorizedAt   *time.Time          `json:"oldest_authorized_at,omitempty"`
	OldestVerificationAt *time.Time          `json:"oldest_verification_at,omitempty"`
	Downloading          int                 `json:"downloading"`
	Imported             int                 `json:"imported"`
	Failed               int                 `json:"failed"`
	Runtime              *twitch.RuntimeWork `json:"runtime,omitempty"`
}

type TwitchCollectorMonitoring interface {
	Status(context.Context) (TwitchCollectorStatus, error)
	Diagnostics(context.Context, string) (TwitchDiagnosticCollector, error)
}

type TwitchMonitoringSnapshot struct {
	CollectorStatus      TwitchCollectorStatus     `json:"collector_status"`
	CollectorObservedAt  *time.Time                `json:"collector_observed_at,omitempty"`
	Counts               []twitch.StageCount       `json:"counts"`
	ActiveTasks          []twitch.ActiveTask       `json:"active_tasks"`
	ActiveTotal          int                       `json:"active_total"`
	OldestRenderQueuedAt *time.Time                `json:"oldest_render_queued_at,omitempty"`
	Discovery            []json.RawMessage         `json:"discovery"`
	DiagnosticsErrors    []TwitchMonitoringFailure `json:"diagnostics_errors"`
	FlowsTotal           int                       `json:"flows_total"`
	FlowsPerTick         int                       `json:"flows_per_tick"`
	FlowsAttempted       int                       `json:"flows_attempted"`
	MainFlowsObserved    int                       `json:"main_flows_observed"`
	NextFlowID           string                    `json:"next_flow_id,omitempty"`
}

type TwitchMonitoringFailure struct {
	Component string `json:"component"`
	FlowID    string `json:"flow_id,omitempty"`
	FlowName  string `json:"flow_name,omitempty"`
	Message   string `json:"message"`
}
