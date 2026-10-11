package service

import (
	"terex/twitch-collector/internal/domain"
	"time"
)

// Verification availability has its own budget; downloading the same bytes
// again cannot repair a temporarily unavailable GPU/verification service.
func CompletionRetryAt(attempt int, f *domain.Failure, now time.Time) *time.Time {
	if !f.Retryable || f.Code == "invalid_media" || f.Code == "upload_generation_changed" || f.Code == "source_rejected" {
		return nil
	}
	delays := []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute}
	index := attempt - 1
	if index < 0 {
		index = 0
	}
	if index >= len(delays) {
		index = len(delays) - 1
	}
	delay := delays[index]
	if f.RetryAfter > delay {
		delay = f.RetryAfter
	}
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	when := now.Add(delay)
	return &when
}
