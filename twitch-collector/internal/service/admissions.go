package service

import (
	"context"
	"fmt"
	"terex/proto/twitch"
	"time"
)

func (s *Service) queueAdmissions(ctx context.Context, cfg twitch.CollectorConfig) (int, error) {
	s.mu.Lock()
	if time.Since(s.lastAdmissionsAt) < time.Minute {
		s.mu.Unlock()
		return 0, nil
	}
	s.lastAdmissionsAt = time.Now()
	s.mu.Unlock()
	api, ok := s.terex.(interface {
		Admissions(context.Context, twitch.AdmissionRequest) (twitch.AdmissionResult, error)
	})
	if !ok {
		return 0, fmt.Errorf("coordinator admissions unavailable")
	}
	store, ok := s.store.(interface {
		QueueAdmissions(context.Context, []twitch.DownloadGrant, time.Time) (int, error)
	})
	if !ok {
		return 0, fmt.Errorf("durable admissions queue unavailable")
	}
	result, err := api.Admissions(ctx, twitch.AdmissionRequest{Epoch: cfg.Epoch, Limit: cfg.Settings.DailyDownloads})
	if err != nil {
		s.mu.Lock()
		s.lastAdmissionsAt = time.Time{}
		s.mu.Unlock()
		return 0, err
	}
	if result.Mode != "enforce" {
		return 0, nil
	}
	return store.QueueAdmissions(ctx, result.Grants, time.Now().UTC())
}
