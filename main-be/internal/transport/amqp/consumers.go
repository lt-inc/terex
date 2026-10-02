// Package amqp consumes events from gpu-be and hands them to services.
package amqp

import (
	"context"
	"encoding/json"
	"log/slog"

	"terex/main-be/internal/service/jobs"
	"terex/main-be/internal/service/publish"
	"terex/proto/amqpx"
	"terex/proto/msg"
)

func handle[T any](queue string, fn func(context.Context, T) error) amqpx.Handler {
	return func(ctx context.Context, body []byte) error {
		var m T
		if err := json.Unmarshal(body, &m); err != nil {
			slog.ErrorContext(ctx, "bad message", "queue", queue, "err", err)
			return err
		}
		if err := fn(ctx, m); err != nil {
			slog.ErrorContext(ctx, "handler failed", "queue", queue, "err", err)
			return err
		}
		return nil
	}
}

// Run declares queues and consumes until ctx is done.
func Run(ctx context.Context, b *amqpx.Broker, jobSvc *jobs.Service, pub *publish.Manager) error {
	for _, q := range []string{msg.QueueProcessVideo, msg.QueuePublishClip, msg.QueueClipsReady, msg.QueueJobStatus, msg.QueuePublishResult} {
		if err := b.Declare(q); err != nil {
			return err
		}
	}
	consumers := map[string]amqpx.Handler{
		msg.QueueClipsReady:    handle(msg.QueueClipsReady, pub.HandleClipsReady),
		msg.QueueJobStatus:     handle(msg.QueueJobStatus, jobSvc.HandleStatus),
		msg.QueuePublishResult: handle(msg.QueuePublishResult, pub.HandleResult),
	}
	done := make(chan struct{}, len(consumers))
	for q, h := range consumers {
		go func() { _ = b.Consume(ctx, q, 4, h); done <- struct{}{} }()
	}
	for range consumers {
		<-done
	}
	return nil
}
