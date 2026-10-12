package service

import (
	"context"
	"fmt"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

func (s *Service) beginResetWorker(ctx context.Context, epoch int64) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resetting || epoch != s.resetEpoch {
		return nil, nil, domain.ErrPaused
	}
	ctx, cancel := context.WithCancelCause(ctx)
	s.activeSequence++
	id := fmt.Sprintf("reset-worker-%d", s.activeSequence)
	s.active[id] = cancel
	release := func() { cancel(nil); s.mu.Lock(); delete(s.active, id); s.mu.Unlock() }
	return ctx, release, nil
}

// Acknowledgement follows cancellation AND actual worker/check completion. The
// control plane cannot start deleting based only on a request to stop yt-dlp.
func (s *Service) handleReset(ctx context.Context, cfg twitch.CollectorConfig) (bool, error) {
	s.resetMu.Lock()
	defer s.resetMu.Unlock()
	s.mu.Lock()
	changed := cfg.Epoch != s.resetEpoch
	if cfg.Maintenance == nil && !changed {
		s.resetting = false
		s.mu.Unlock()
		return false, nil
	}
	s.resetting = true
	if s.checkCancel != nil {
		s.checkCancel()
	}
	for _, cancel := range s.active {
		cancel(domain.ErrPaused)
	}
	drained := !s.checking && len(s.active) == 0
	s.mu.Unlock()
	if !drained {
		return true, nil
	}
	store, ok := s.store.(interface {
		ResetTwitchPool(context.Context, int64) error
	})
	if !ok {
		return true, fmt.Errorf("collector reset storage is unavailable")
	}
	if err := store.ResetTwitchPool(ctx, cfg.Epoch); err != nil {
		return true, err
	}
	s.mu.Lock()
	s.resetEpoch = cfg.Epoch
	s.flowStarted = map[string]time.Time{}
	s.lastStarted = time.Time{}
	s.configFingerprint = ""
	s.lastTemporalAt = time.Time{}
	s.lastPressureAt = time.Time{}
	s.grantCursor = ""
	s.lastAdmissionsAt = time.Time{}
	s.admissionsRetryAt = time.Time{}
	s.admissionsFailures = 0
	s.mu.Unlock()
	if cfg.Maintenance != nil {
		api, ok := s.terex.(interface {
			AcknowledgeReset(context.Context, twitch.ResetCollectorAck) error
		})
		if !ok {
			return true, fmt.Errorf("collector reset acknowledgement is unavailable")
		}
		if err := api.AcknowledgeReset(ctx, twitch.ResetCollectorAck{ID: cfg.Maintenance.ID, Epoch: cfg.Epoch}); err != nil {
			return true, err
		}
		s.log.InfoContext(ctx, "collector reset acknowledged", "reset_id", cfg.Maintenance.ID, "epoch", cfg.Epoch)
		return true, nil
	}
	s.mu.Lock()
	s.resetting = false
	s.mu.Unlock()
	return false, nil
}
