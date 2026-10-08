package domain

import (
	"context"
	"terex/proto/twitch"
	"terex/proto/workflow"
	"time"
)

type TwitchResetRepo interface {
	PreviewTwitchReset(context.Context, time.Time) (twitch.ResetPreview, error)
	StartTwitchReset(context.Context, twitch.ResetRequest, time.Time) (twitch.ResetStatus, error)
	TwitchReset(context.Context) (twitch.ResetStatus, error)
	AcknowledgeTwitchReset(context.Context, twitch.ResetCollectorAck, time.Time) error
	AdvanceTwitchReset(context.Context, string, bool, time.Time) (twitch.ResetStatus, error)
	QueueTwitchResetObjects(context.Context, string, time.Time) error
	PendingTwitchResetObjects(context.Context, string, int) ([]workflow.StorageObject, error)
	CheckTwitchResetObject(context.Context, string, workflow.ObjectRef) (bool, error)
	FinishTwitchResetObject(context.Context, string, workflow.ObjectRef, error, time.Time) error
	ClearTwitchReset(context.Context, string, time.Time) error
	FailTwitchReset(context.Context, string, string, time.Time) error
}
