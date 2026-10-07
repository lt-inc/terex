package domain

import (
	"context"
	"fmt"
	"terex/proto/twitch"
	"time"
)

// A capacity wait is not a failed network download and consumes no retry.
type PreparationWaitError struct{ twitch.PreparationWait }

func (e *PreparationWaitError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
func (e *PreparationWaitError) Unwrap() error { return ErrConflict }

type TwitchPolicyViolation struct {
	PublicationID   int64      `json:"publication_id"`
	ClipID          string     `json:"clip_id"`
	FlowID          string     `json:"flow_id"`
	Automatic       bool       `json:"automatic"`
	PublishAt       time.Time  `json:"publish_at"`
	SourceCreatedAt *time.Time `json:"source_created_at,omitempty"`
	Deadline        *time.Time `json:"deadline,omitempty"`
	Code            string     `json:"code"`
	Message         string     `json:"message"`
	Protected       bool       `json:"protected"`
}
type TwitchPolicyPreview struct {
	Mode       string                  `json:"mode"`
	Violations []TwitchPolicyViolation `json:"violations"`
	Held       int                     `json:"held"`
}
type TwitchPolicyRepo interface {
	FlowCapacity(context.Context, string, time.Time) (twitch.FlowCapacity, error)
	PreviewTwitchPolicy(context.Context, time.Time) (TwitchPolicyPreview, error)
	ApplyTwitchPolicy(context.Context, time.Time) (TwitchPolicyPreview, error)
	ListTwitchFlowsIncludingArchived(context.Context) ([]twitch.Flow, error)
	RestoreTwitchFlow(context.Context, string, time.Time) (twitch.Flow, error)
	AbortFlowImport(context.Context, string, int64, string, time.Time) error
}
