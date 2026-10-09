package twitch

import (
	"context"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

func (s *Service) ObservationCandidates(ctx context.Context, id string, limit int) ([]twitch.Source, error) {
	r, ok := s.Repo.(interface {
		TwitchObservationCandidates(context.Context, string, int, time.Time) ([]twitch.Source, error)
	})
	if !ok {
		return nil, domain.ErrNotFound
	}
	return r.TwitchObservationCandidates(ctx, id, limit, s.now())
}
