package twitch

import (
	"context"
	"terex/main-be/internal/domain"
	t "terex/proto/twitch"
)

func (s *Service) policyRepo() (domain.TwitchPolicyRepo, error) {
	r, ok := s.Repo.(domain.TwitchPolicyRepo)
	if !ok {
		return nil, domain.ErrNotFound
	}
	return r, nil
}
func (s *Service) Capacity(ctx context.Context, id string) (t.FlowCapacity, error) {
	r, e := s.policyRepo()
	if e != nil {
		return t.FlowCapacity{}, e
	}
	return r.FlowCapacity(ctx, id, s.now())
}
func (s *Service) PolicyPreview(ctx context.Context) (domain.TwitchPolicyPreview, error) {
	r, e := s.policyRepo()
	if e != nil {
		return domain.TwitchPolicyPreview{}, e
	}
	return r.PreviewTwitchPolicy(ctx, s.now())
}
func (s *Service) ApplyPolicy(ctx context.Context) (domain.TwitchPolicyPreview, error) {
	r, e := s.policyRepo()
	if e != nil {
		return domain.TwitchPolicyPreview{}, e
	}
	return r.ApplyTwitchPolicy(ctx, s.now())
}
func (s *Service) FlowsIncludingArchived(ctx context.Context) ([]t.Flow, error) {
	r, e := s.policyRepo()
	if e != nil {
		return nil, e
	}
	return r.ListTwitchFlowsIncludingArchived(ctx)
}
func (s *Service) RestoreFlow(ctx context.Context, id string) (t.Flow, error) {
	r, e := s.policyRepo()
	if e != nil {
		return t.Flow{}, e
	}
	return r.RestoreTwitchFlow(ctx, id, s.now())
}
func (s *Service) AbortImport(ctx context.Context, id string, generation int64, reason string) error {
	// Generation zero is a coordinator admission that failed before upload.
	// The repository still requires an exact generation match to fence retries.
	if generation < 0 || len(reason) > 1000 {
		return domain.ErrInvalid
	}
	r, e := s.policyRepo()
	if e != nil {
		return e
	}
	return r.AbortFlowImport(ctx, id, generation, reason, s.now())
}
