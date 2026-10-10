package service

import (
	"context"
	"time"

	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

const configFreshness = 2 * time.Minute

type configObservation struct {
	startedAt   time.Time
	observedAt  time.Time
	settings    twitch.Settings
	maintenance bool
	reason      string
	err         error
}

// These observations come from actual work, never from a monitoring request.
// Ordering by request start prevents a late response from replacing newer state.
func (s *Service) observeConfig(observation configObservation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if observation.startedAt.After(s.configObservation.startedAt) {
		s.configObservation = observation
	}
}

func (s *Service) collectorConfig(ctx context.Context, api domain.FlowTerex) (twitch.CollectorConfig, error) {
	started := time.Now()
	cfg, err := api.CollectorConfig(ctx)
	ready := s.twitch != nil && s.twitch.Ready()
	s.observeConfig(configObservation{
		startedAt: started, observedAt: time.Now().UTC(), settings: cfg.Settings,
		maintenance: cfg.Maintenance != nil, reason: flowBlockReason(cfg, err, ready), err: err,
	})
	return cfg, err
}

func (s *Service) settings(ctx context.Context) (domain.Settings, error) {
	started := time.Now()
	cfg, err := s.terex.Settings(ctx)
	// A legacy task can still ask for v1 settings during a rolling deployment.
	// Those settings do not describe active flows or reset maintenance.
	if _, flows := s.terex.(domain.FlowTerex); flows {
		return cfg, err
	}
	ready := s.twitch != nil && s.twitch.Ready()
	s.observeConfig(configObservation{
		startedAt: started, observedAt: time.Now().UTC(), settings: cfg,
		reason: settingsBlockReason(cfg, err, ready), err: err,
	})
	return cfg, err
}
