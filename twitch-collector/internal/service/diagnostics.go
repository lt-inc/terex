package service

import (
	"context"
	"errors"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

func (s *Service) FlowDiagnostics(ctx context.Context, id string) (domain.FlowDiagnostics, error) {
	if _, ok := s.terex.(domain.FlowTerex); !ok {
		return domain.FlowDiagnostics{}, errors.New("flow diagnostics unavailable")
	}
	s.mu.Lock()
	config := s.configObservation
	s.mu.Unlock()
	if config.err != nil || config.observedAt.IsZero() || time.Since(config.observedAt) > configFreshness {
		return domain.FlowDiagnostics{}, errors.New("flow diagnostics unavailable: no recent Terex settings observation")
	}
	repo, ok := s.store.(interface {
		FlowDiagnostics(context.Context, string, twitch.Settings, time.Time) (domain.FlowDiagnostics, error)
	})
	if !ok {
		return domain.FlowDiagnostics{}, errors.New("flow diagnostics unavailable")
	}
	out, err := repo.FlowDiagnostics(ctx, id, config.settings, time.Now().UTC())
	if err != nil {
		return out, err
	}
	if reports, ok := s.twitch.(interface {
		DiscoveryReports() []domain.DiscoveryReport
	}); ok {
		for _, report := range reports.DiscoveryReports() {
			if report.FlowID == id {
				out.Discovery = append(out.Discovery, report)
			}
		}
	}
	return out, nil
}
