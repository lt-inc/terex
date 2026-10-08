package twitch

import "time"

const ResetConfirmation = "RESET TWITCH"

type ResetMaintenance struct {
	ID    string `json:"id"`
	Epoch int64  `json:"epoch"`
	State string `json:"state"`
}
type ResetCounts struct {
	Imports      int64 `json:"imports"`
	Clips        int64 `json:"clips"`
	Revisions    int64 `json:"revisions"`
	Publications int64 `json:"publications"`
	Jobs         int64 `json:"jobs"`
	Objects      int64 `json:"objects"`
	Bytes        int64 `json:"bytes"`
}
type ResetBlocker struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	PublicationID int64  `json:"publication_id,omitempty"`
}
type ResetPreview struct {
	PreviewToken       string         `json:"preview_token"`
	ExpiresAt          time.Time      `json:"expires_at"`
	ConfirmationPhrase string         `json:"confirmation_phrase"`
	Counts             ResetCounts    `json:"counts"`
	Blockers           []ResetBlocker `json:"blockers"`
}
type ResetRequest struct {
	PreviewToken string `json:"preview_token"`
	Confirmation string `json:"confirmation"`
}
type ResetStatus struct {
	ID               string         `json:"id"`
	State            string         `json:"state"`
	Epoch            int64          `json:"epoch"`
	CollectorAck     bool           `json:"collector_ack"`
	GPUDrained       bool           `json:"gpu_drained"`
	Counts           ResetCounts    `json:"counts"`
	DeletedObjects   int64          `json:"deleted_objects"`
	DeletedBytes     int64          `json:"deleted_bytes"`
	RemainingObjects int64          `json:"remaining_objects"`
	PreservedObjects int64          `json:"preserved_objects"`
	Blockers         []ResetBlocker `json:"blockers"`
	Error            string         `json:"error,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
}
type ResetCollectorAck struct {
	ID    string `json:"id"`
	Epoch int64  `json:"epoch"`
}
