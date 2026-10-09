package service

import (
	"context"
	"errors"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

type recoveryStore interface {
	RecoverableImports(context.Context, int64) ([]domain.FailedImport, error)
	FailedForRetry(context.Context, string) (domain.FailedImport, error)
	RevalidationAttempted(context.Context, domain.FailedImport) error
	ApplyRevalidation(context.Context, domain.FailedImport, twitch.RevalidationReceipt) error
}
type recoveryAPI interface {
	RevalidateImport(context.Context, string, twitch.RevalidateImportRequest) (twitch.RevalidationReceipt, error)
}
type stagedImportAPI interface {
	ImportStatus(context.Context, string) (twitch.Import, error)
	PutImport(context.Context, twitch.Import, domain.Download, func(context.Context) error) error
	CompleteImport(context.Context, twitch.Import, domain.UploadedMedia) (twitch.Import, error)
}

func (s *Service) revalidateFailure(ctx context.Context, api recoveryAPI, store recoveryStore, f domain.FailedImport, manual bool) error {
	if err := store.RevalidationAttempted(ctx, f); err != nil {
		return err
	}
	reason := f.ErrorCode
	if reason == "" {
		reason = "manual_retry"
	}
	receipt, err := api.RevalidateImport(ctx, f.Reservation.ImportID, twitch.RevalidateImportRequest{
		Epoch: f.Reservation.Epoch, AdmissionSequence: f.Reservation.AdmissionSequence, UploadGeneration: f.Reservation.UploadGeneration,
		FailureToken: f.FailureToken, FlowID: f.FlowID, FlowVersion: f.FlowVersion, Reason: reason, Manual: manual, Uploaded: f.Uploaded != nil,
	})
	if err != nil {
		return err
	}
	if err = store.ApplyRevalidation(ctx, f, receipt); err != nil {
		return err
	}
	s.log.InfoContext(ctx, "collector source revalidated", "clip_id", f.ClipID, "flow_id", receipt.FlowID, "flow_version", receipt.FlowVersion, "state", receipt.State, "manual", manual)
	return nil
}

func (s *Service) reconcileRevalidations(ctx context.Context, api domain.FlowTerex, epoch int64) {
	store, ok := s.store.(recoveryStore)
	remote, ready := api.(recoveryAPI)
	if !ok || !ready {
		return
	}
	s.mu.Lock()
	if time.Since(s.lastRevalidationAt) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastRevalidationAt = time.Now()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := store.RecoverableImports(ctx, epoch)
	if err != nil {
		s.log.WarnContext(ctx, "collector revalidation scan unavailable", errorFields(err)...)
		return
	}
	for _, f := range rows {
		if ctx.Err() != nil {
			return
		}
		if err = s.revalidateFailure(ctx, remote, store, f, false); err != nil {
			var remote *domain.Failure
			if errors.As(err, &remote) && remote.HTTPStatus == 409 {
				s.log.InfoContext(ctx, "collector revalidation waiting", "clip_id", f.ClipID, "reason_code", remote.Code, "reason", remote.Message)
			} else {
				s.log.WarnContext(ctx, "collector revalidation unavailable", "clip_id", f.ClipID, "reason", safeErrorMessage(err, "Повторная проверка временно недоступна"))
			}
		}
	}
}

func (s *Service) retryFlowClip(ctx context.Context, id string) error {
	store, ok := s.store.(recoveryStore)
	api, ready := s.terex.(recoveryAPI)
	flows, configured := s.terex.(domain.FlowTerex)
	if !ok || !ready || !configured {
		return domain.ErrConflict
	}
	cfg, err := flows.CollectorConfig(ctx)
	if err != nil {
		return err
	}
	if cfg.Maintenance != nil || !cfg.Settings.Enabled {
		return domain.ErrPaused
	}
	work, release, err := s.beginResetWorker(ctx, cfg.Epoch)
	if err != nil {
		return err
	}
	defer release()
	if err = syncFlowConfig(work, s.store.(domain.FlowStore), cfg); err != nil {
		return err
	}
	failure, err := store.FailedForRetry(work, id)
	if err != nil {
		return err
	}
	if failure.Reservation.Epoch != cfg.Epoch {
		return domain.ErrConflict
	}
	if failure.Reservation.UploadGeneration > 0 {
		if aborter, ok := s.terex.(interface {
			AbortFailedImport(context.Context, domain.FailedImport) error
		}); ok {
			if err = aborter.AbortFailedImport(work, failure); err != nil {
				var remote *domain.Failure
				// A previous revalidation may have committed while its response was
				// lost. Repeating it returns the durable receipt without a reset.
				if !errors.As(err, &remote) || remote.HTTPStatus != 409 {
					return err
				}
			}
		}
	}
	return s.revalidateFailure(work, api, store, failure, true)
}
