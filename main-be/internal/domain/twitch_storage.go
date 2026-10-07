package domain

import (
	"context"
	"terex/proto/workflow"
	"time"
)

type TwitchStorageItem struct {
	Bucket          string             `json:"bucket"`
	Object          string             `json:"object"`
	Kind            string             `json:"kind"`
	SizeBytes       int64              `json:"size_bytes"`
	State           string             `json:"state"`
	Keep            bool               `json:"keep"`
	Pins            []TwitchStoragePin `json:"pins"`
	ExpiresAt       time.Time          `json:"expires_at"`
	ProtectedReason string             `json:"protected_reason,omitempty"`
	ClipIDs         []string           `json:"clip_ids"`
	ImportIDs       []string           `json:"import_ids"`
	Error           string             `json:"error,omitempty"`
}
type TwitchStoragePin struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type TwitchStorageStats struct {
	CleanupEnabled    bool                  `json:"cleanup_enabled"`
	InventoryComplete bool                  `json:"inventory_complete"`
	PhysicalBytes     int64                 `json:"physical_bytes"`
	ReclaimableBytes  int64                 `json:"reclaimable_bytes"`
	Objects           int                   `json:"objects"`
	ProtectedObjects  int                   `json:"protected_objects"`
	DeletedObjects    int                   `json:"deleted_objects"`
	PendingDeletions  int                   `json:"pending_deletions"`
	FailedDeletions   int                   `json:"failed_deletions"`
	UpdatedAt         time.Time             `json:"updated_at"`
	Items             []TwitchStorageItem   `json:"items"`
	Candidates        []TwitchStorageItem   `json:"candidates,omitempty"`
	Pressure          TwitchStoragePressure `json:"pressure"`
}
type TwitchStoragePressure struct {
	Required       bool      `json:"required"`
	State          string    `json:"state"`
	AvailableBytes *int64    `json:"available_bytes,omitempty"`
	TotalBytes     *int64    `json:"capacity_bytes,omitempty"`
	ObservedAt     time.Time `json:"observed_at"`
	Error          string    `json:"error,omitempty"`
	Reason         string    `json:"reason,omitempty"`
}
type TwitchCleanupResult struct {
	DryRun            bool  `json:"dry_run"`
	Queued            int   `json:"queued"`
	Deleted           int   `json:"deleted"`
	Failed            int   `json:"failed"`
	ReclaimableBytes  int64 `json:"reclaimable_bytes"`
	InventoryComplete bool  `json:"inventory_complete"`
}
type TwitchStorageRepo interface {
	SyncTwitchStorage(context.Context, []workflow.StorageObject, time.Time) error
	TwitchStorage(context.Context, time.Time) (TwitchStorageStats, error)
	QueueTwitchCleanup(context.Context, time.Time, int) (int, error)
	PendingTwitchDeletion(context.Context, time.Time, int) ([]workflow.ObjectRef, error)
	CheckTwitchDeletion(context.Context, workflow.ObjectRef, time.Time) (bool, error)
	FinishTwitchDeletion(context.Context, workflow.ObjectRef, error, time.Time) error
	KeepTwitchStorage(context.Context, string, string, bool) error
	SaveTwitchStoragePressure(context.Context, TwitchStoragePressure) error
}
