// Package cron runs the periodic jobs: channel monitoring and the publish scheduler.
package cron

import (
	"context"
	"log/slog"
	"time"

	"terex/proto/telemetry"
)

type Tick interface {
	Tick(ctx context.Context) error
}

type Func func(context.Context) error

func (f Func) Tick(ctx context.Context) error { return f(ctx) }

// Every runs t immediately and then on every interval until ctx is done.
func Every(ctx context.Context, name string, interval time.Duration, t Tick) {
	run := func() {
		tctx, span := telemetry.Start(ctx, "cron "+name)
		err := t.Tick(tctx)
		telemetry.End(span, err)
		if err != nil && ctx.Err() == nil {
			slog.ErrorContext(tctx, "cron tick failed", "job", name, "err", err)
		}
	}
	run()
	tk := time.NewTicker(interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			run()
		}
	}
}
