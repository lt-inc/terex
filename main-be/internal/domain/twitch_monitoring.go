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
	Available        bool                `json:"available"`
	Running          bool                `json:"running"`
	Paused           bool                `json:"paused"`
	CredentialsReady bool                `json:"credentials_ready"`
	LastCheckAt      *time.Time          `json:"last_check_at,omitempty"`
	LastError        string              `json:"last_error,omitempty"`
	BlockReason      string              `json:"block_reason,omitempty"`
	Channels         int                 `json:"channels"`
	Queued           int                 `json:"queued"`
	Downloading      int                 `json:"downloading"`
	Imported         int                 `json:"imported"`
	Failed           int                 `json:"failed"`
	Runtime          *twitch.RuntimeWork `json:"runtime,omitempty"`
}

type TwitchCollectorMonitoring interface {
	Status(context.Context) (TwitchCollectorStatus, error)
	Diagnostics(context.Context, string) (TwitchDiagnosticCollector, error)
}

type TwitchMonitoringSnapshot struct {
	CollectorStatus TwitchCollectorStatus `json:"collector_status"`
	Counts          []twitch.StageCount   `json:"counts"`
	ActiveTasks     []twitch.ActiveTask   `json:"active_tasks"`
	ActiveTotal     int                   `json:"active_total"`
	Discovery       []json.RawMessage     `json:"discovery"`
}
