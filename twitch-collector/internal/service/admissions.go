package service

import (
	"context"
	"fmt"
	"terex/proto/twitch"
	"time"
)

func (s *Service) queueAdmissions(ctx context.Context, cfg twitch.CollectorConfig) (queued int, err error) {
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	started := time.Now()
	s.mu.Lock()
	if !s.admissionsRetryAt.IsZero() && started.Before(s.admissionsRetryAt) || s.admissionsRetryAt.IsZero() && started.Sub(s.lastAdmissionsAt) < s.timeAdmissions {
		s.mu.Unlock()
		return 0, nil
	}
	s.lastAdmissionsAt = started
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if err != nil {
			s.admissionsFailures++
			delay := 15 * time.Second
			for i := 1; i < s.admissionsFailures && delay < time.Minute; i++ {
				delay *= 2
			}
			s.admissionsRetryAt = time.Now().Add(delay)
		} else {
			s.admissionsFailures = 0
			s.admissionsRetryAt = time.Time{}
			s.lastAdmissionsAt = time.Now()
		}
		retryAt := s.admissionsRetryAt
		s.mu.Unlock()
		if err != nil {
			s.log.WarnContext(ctx, "collector admissions deferred", append([]any{"epoch", cfg.Epoch, "duration_ms", time.Since(started).Milliseconds(), "next_attempt_at", retryAt}, errorFields(err)...)...)
		}
	}()
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
		return 0, err
	}
	if result.Mode != "enforce" {
		return 0, nil
	}
	queued, err = store.QueueAdmissions(ctx, result.Grants, time.Now().UTC())
	if err == nil {
		s.log.InfoContext(ctx, "collector admissions queued", "epoch", cfg.Epoch, "grants", len(result.Grants), "queued", queued, "daily_remaining", result.DailyRemaining, "duration_ms", time.Since(started).Milliseconds())
	}
	return queued, err
}
