package service

import (
	"context"
	"time"

	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

const configFreshness = 30 * time.Second

type configObservation struct {
	sequence    uint64
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
	s.mu.Lock()
	cfg, observed, running := s.configCache, s.configObservation, s.coordinationStarted
	s.mu.Unlock()
	if !observed.observedAt.IsZero() && time.Since(observed.observedAt) <= s.configMaxAge {
		return cfg, nil
	}
	if running {
		return cfg, &domain.Failure{Kind: "config", Code: "config_stale", Message: "Нет свежих настроек Terex; новые операции приостановлены", Retryable: true}
	}
	return s.fetchCollectorConfig(ctx, api)
}

func (s *Service) fetchCollectorConfig(ctx context.Context, api domain.FlowTerex) (twitch.CollectorConfig, error) {
	started := time.Now()
	s.mu.Lock()
	s.configFetchSequence++
	sequence := s.configFetchSequence
	s.mu.Unlock()
	cfg, err := api.CollectorConfig(ctx)
	if err != nil {
		// Keep the last successful control snapshot, but let its age close the
		// gate. A transient error never replaces known epoch/reset information.
		s.mu.Lock()
		if sequence >= s.configObservation.sequence {
			s.configObservation.err = err
			// The last confirmed snapshot remains usable until its freshness
			// deadline. Report the failed refresh separately from that state.
		}
		s.mu.Unlock()
		return cfg, err
	}
	ready := s.twitch != nil && s.twitch.Ready()
	observation := configObservation{
		sequence: sequence, startedAt: started, observedAt: time.Now().UTC(), settings: cfg.Settings,
		maintenance: cfg.Maintenance != nil, reason: flowBlockReason(cfg, err, ready), err: err,
	}
	s.mu.Lock()
	if sequence >= s.configObservation.sequence {
		s.configCache = cfg
		s.configObservation = observation
	}
	s.mu.Unlock()
	s.log.DebugContext(ctx, "collector config refreshed", "epoch", cfg.Epoch, "flows", len(cfg.Flows), "duration_ms", time.Since(started).Milliseconds())
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
