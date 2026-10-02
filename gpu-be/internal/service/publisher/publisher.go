// Package publisher is a stub: it acknowledges every publish request without posting anywhere.
// A real implementation (YouTube, TikTok, ...) can replace Stub behind the Publisher interface.
package publisher

import (
	"context"

	"terex/gpu-be/internal/domain"
)

type Publisher interface {
	Publish(ctx context.Context, req domain.PublishClip) (domain.PublishResult, error)
}

type Stub struct{}

func (Stub) Publish(_ context.Context, req domain.PublishClip) (domain.PublishResult, error) {
	return domain.PublishResult{ClipID: req.ClipID, AccountID: req.AccountID, Status: "scheduled", UploadID: "stub-" + req.ClipID}, nil
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
