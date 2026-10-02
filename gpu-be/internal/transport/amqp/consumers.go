// Package amqp wires RabbitMQ queues to the module services. No business logic here.
package amqp

import (
	"context"
	"encoding/json"
	"log/slog"

	"terex/gpu-be/internal/domain"
	"terex/proto/amqpx"
)

type Services struct {
	Preproc interface {
		Handle(context.Context, domain.ProcessVideo) error
	}
	Candidate interface {
		Handle(context.Context, domain.ProcessVideo) error
	}
	Renderer interface {
		Handle(context.Context, domain.ProcessVideo) error
	}
	Publisher interface {
		Handle(context.Context, domain.PublishClip) error
	}
}

func handler[T any](queue string, fn func(context.Context, T) error) amqpx.Handler {
	return func(ctx context.Context, body []byte) error {
		var msg T
		if err := json.Unmarshal(body, &msg); err != nil {
			slog.ErrorContext(ctx, "bad message", "queue", queue, "err", err)
			return err
		}
		if err := fn(ctx, msg); err != nil {
			slog.ErrorContext(ctx, "handler failed", "queue", queue, "err", err)
			return err
		}
		return nil
	}
}

// Run declares all queues and starts one consumer per module; blocks until ctx is done.
func Run(ctx context.Context, b *amqpx.Broker, workers int, s Services) error {
	for _, q := range []string{
		domain.QueueProcessVideo, domain.QueueCandidate, domain.QueueRender, domain.QueuePublishClip,
		domain.QueueClipsReady, domain.QueueJobStatus, domain.QueuePublishResult,
	} {
		if err := b.Declare(q); err != nil {
			return err
		}
	}
	consumers := map[string]amqpx.Handler{
		domain.QueueProcessVideo: handler(domain.QueueProcessVideo, s.Preproc.Handle),
		domain.QueueCandidate:    handler(domain.QueueCandidate, s.Candidate.Handle),
		domain.QueueRender:       handler(domain.QueueRender, s.Renderer.Handle),
		domain.QueuePublishClip:  handler(domain.QueuePublishClip, s.Publisher.Handle),
	}
	done := make(chan struct{}, len(consumers))
	for q, h := range consumers {
		go func() { _ = b.Consume(ctx, q, workers, h); done <- struct{}{} }()
	}
	for range consumers {
		<-done
	}
	return nil
}
