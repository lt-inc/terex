package twitch

import "time"

type AdmissionRequest struct {
	Epoch int64 `json:"epoch"`
	Limit int   `json:"limit"`
}
type DownloadGrant struct {
	AdmissionSequence int64      `json:"admission_sequence,omitempty"`
	RevalidationToken string     `json:"revalidation_token,omitempty"`
	ClipID            string     `json:"clip_id"`
	ImportID          string     `json:"import_id"`
	FlowID            string     `json:"flow_id"`
	FlowVersion       int        `json:"flow_version"`
	Format            string     `json:"format"`
	Source            Source     `json:"source"`
	Epoch             int64      `json:"epoch"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
}
type AdmissionResult struct {
	Mode           string          `json:"mode"`
	BudgetPercent  int             `json:"budget_percent"`
	DailyRemaining int             `json:"daily_remaining"`
	Grants         []DownloadGrant `json:"grants"`
}

// Pressure checks named durable assignments; an omitted admissions page is not
// evidence that an assignment was revoked. It never allocates or renews grants.
type PressureTaskRef struct {
	ClipID            string `json:"clip_id"`
	ImportID          string `json:"import_id"`
	FlowID            string `json:"flow_id"`
	AdmissionSequence int64  `json:"admission_sequence"`
	UploadGeneration  int64  `json:"upload_generation"`
}
type PressureRequest struct {
	Epoch int64             `json:"epoch"`
	Tasks []PressureTaskRef `json:"tasks"`
}
type StoragePressure struct {
	Required       bool      `json:"required"`
	State          string    `json:"state"`
	AvailableBytes *int64    `json:"available_bytes,omitempty"`
	CapacityBytes  *int64    `json:"capacity_bytes,omitempty"`
	ObservedAt     time.Time `json:"observed_at"`
	Reason         string    `json:"reason,omitempty"`
}
type PressureTaskResult struct {
	PressureTaskRef
	GrantState  string     `json:"grant_state"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ImportState string     `json:"import_state"`
	MediaState  string     `json:"media_state"`
	ReasonCode  string     `json:"reason_code"`
}
type PressureResult struct {
	Epoch           int64                `json:"epoch"`
	Maintenance     bool                 `json:"maintenance"`
	Enabled         bool                 `json:"enabled"`
	ObservedAt      time.Time            `json:"observed_at"`
	PendingPhysical int                  `json:"pending_physical"`
	BacklogLimit    int                  `json:"backlog_limit"`
	Storage         StoragePressure      `json:"storage"`
	Tasks           []PressureTaskResult `json:"tasks"`
}
