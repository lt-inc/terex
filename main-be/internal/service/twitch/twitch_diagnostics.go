package twitch

import (
	"context"
	"terex/main-be/internal/domain"
)

func (s *Service) Diagnostics(ctx context.Context, id string) (domain.TwitchFlowDiagnostics, error) {
	r, ok := s.Repo.(domain.TwitchDiagnosticsRepo)
	if !ok {
		return domain.TwitchFlowDiagnostics{}, domain.ErrNotFound
	}
	return r.TwitchFlowDiagnostics(ctx, id, s.now())
}
