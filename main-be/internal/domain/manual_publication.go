package domain

import (
	"context"
	"time"
)

// ManualPublishRequest approves only the reviewed snapshot, never a flow target.
// Media references are resolved by the repository; the client cannot supply URLs.
type ManualPublishRequest struct {
	RequestID         string `json:"request_id"`
	RevisionID        int64  `json:"revision_id"`
	Version           int    `json:"expected_version"`
	MediaKind         string `json:"media_kind"`
	AccountID         int64  `json:"account_id"`
	Platform          string `json:"platform"`
	Caption           string `json:"caption"`
	Visibility        string `json:"visibility"`
	Reviewed          bool   `json:"reviewed"`
	FreshnessOverride bool   `json:"freshness_override"`
}

type PublicationVariant struct {
	MediaKind string   `json:"media_kind"`
	Eligible  bool     `json:"eligible"`
	Reason    string   `json:"reason"`
	Platforms []string `json:"platforms"`
}

type ClipPublicationOptions struct {
	RevisionID   int64                `json:"revision_id"`
	Version      int                  `json:"expected_version"`
	Title        string               `json:"title"`
	Caption      string               `json:"caption"`
	Variants     []PublicationVariant `json:"variants"`
	Publications []PublicationRecord  `json:"publications"`
}

type ManualPublicationRepo interface {
	ClipPublicationOptions(context.Context, string) (ClipPublicationOptions, error)
	CreateManualPublication(context.Context, string, ManualPublishRequest, time.Time) (PublicationRecord, error)
}
