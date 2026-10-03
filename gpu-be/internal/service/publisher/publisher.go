// Package publisher preserves the legacy queue as a manual workflow. Real API
// publications use the fenced workflow publisher, never legacy cookie files.
package publisher

import (
	"context"

	"terex/gpu-be/internal/domain"
)

type Publisher interface {
	Publish(ctx context.Context, req domain.PublishClip) (domain.PublishResult, error)
}

type Manual struct{}

func (Manual) Publish(_ context.Context, req domain.PublishClip) (domain.PublishResult, error) {
	return domain.PublishResult{ClipID: req.ClipID, AccountID: req.AccountID, Status: "manual_required", Error: "legacy account requires manual publication or Google OAuth connection"}, nil
}

type Service struct {
	Pub    Publisher
	Broker domain.Broker
}

func (s *Service) Handle(ctx context.Context, req domain.PublishClip) error {
	res, err := s.Pub.Publish(ctx, req)
	if err != nil {
		res = domain.PublishResult{ClipID: req.ClipID, AccountID: req.AccountID, Status: "failed", Error: err.Error()}
	}
	return s.Broker.Publish(ctx, domain.QueuePublishResult, res)
}
