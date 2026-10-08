package twitch

import (
	"context"
	"fmt"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

func (s *Service) DownloadAdmissions(ctx context.Context, q twitch.AdmissionRequest) (twitch.AdmissionResult, error) {
	if q.Limit < 1 || q.Limit > 100 {
		return twitch.AdmissionResult{}, domain.ErrInvalid
	}
	repo, ok := s.Repo.(interface {
		AllocateTwitchDownloads(context.Context, twitch.AdmissionRequest, time.Time) (twitch.AdmissionResult, error)
	})
	if !ok {
		return twitch.AdmissionResult{}, fmt.Errorf("preparation allocator unavailable")
	}
	return repo.AllocateTwitchDownloads(ctx, q, s.now())
}
