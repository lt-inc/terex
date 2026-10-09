package domain

import (
	"context"
	"terex/proto/twitch"
	"time"
)

type TwitchRevalidationRepo interface {
	RevalidateFlowImport(context.Context, string, twitch.RevalidateImportRequest, time.Time) (twitch.RevalidationReceipt, error)
	AbortFlowImportFenced(context.Context, string, twitch.AbortImportRequest, time.Time) error
}
