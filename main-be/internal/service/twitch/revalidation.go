package twitch

import (
	"context"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
)

func (s *Service) RevalidateImport(ctx context.Context, id string, q twitch.RevalidateImportRequest) (twitch.RevalidationReceipt, error) {
	if id == "" || q.Epoch < 0 || q.AdmissionSequence < 0 || q.UploadGeneration < 0 || len(q.FailureToken) < 16 || len(q.FailureToken) > 128 || strings.TrimSpace(q.FlowID) == "" || q.FlowVersion < 1 || len(q.Reason) > 1000 {
		return twitch.RevalidationReceipt{}, domain.ErrInvalid
	}
	r, ok := s.Repo.(domain.TwitchRevalidationRepo)
	if !ok {
		return twitch.RevalidationReceipt{}, domain.ErrNotFound
	}
	return r.RevalidateFlowImport(ctx, id, q, s.now())
}

func (s *Service) AbortImportFenced(ctx context.Context, id string, q twitch.AbortImportRequest) error {
	if q.Epoch < 0 || q.AdmissionSequence < 0 || q.UploadGeneration < 0 || len(q.Reason) > 1000 {
		return domain.ErrInvalid
	}
	r, ok := s.Repo.(domain.TwitchRevalidationRepo)
	if !ok {
		return domain.ErrNotFound
	}
	return r.AbortFlowImportFenced(ctx, id, q, s.now())
}
