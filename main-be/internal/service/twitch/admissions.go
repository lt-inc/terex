package twitch

import (
	"context"
	"fmt"
	"log/slog"
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

func (s *Service) CollectorPressure(ctx context.Context, q twitch.PressureRequest) (twitch.PressureResult, error) {
	if q.Epoch < 0 || len(q.Tasks) > 200 {
		return twitch.PressureResult{}, domain.ErrInvalid
	}
	for _, ref := range q.Tasks {
		if ref.ClipID == "" || ref.ImportID == "" || ref.FlowID == "" || ref.AdmissionSequence < 0 || ref.UploadGeneration < 0 {
			return twitch.PressureResult{}, domain.ErrInvalid
		}
	}
	repo, ok := s.Repo.(interface {
		TwitchCollectorPressure(context.Context, twitch.PressureRequest, time.Time) (twitch.PressureResult, error)
	})
	if !ok {
		return twitch.PressureResult{}, fmt.Errorf("collector pressure unavailable")
	}
	started := time.Now()
	out, err := repo.TwitchCollectorPressure(ctx, q, s.now())
	slog.InfoContext(ctx, "collector pressure observed", "epoch", out.Epoch, "task_count", len(q.Tasks), "pending_physical", out.PendingPhysical, "backlog_limit", out.BacklogLimit, "duration_ms", time.Since(started).Milliseconds(), "err", err)
	return out, err
}
