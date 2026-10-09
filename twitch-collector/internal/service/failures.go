package service

import (
	"context"
	"errors"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

type failureStore interface {
	FailedImports(context.Context, int64) ([]domain.FailedImport, error)
	FailedImportSynced(context.Context, domain.FailedImport) error
}

type importAborter interface {
	AbortImport(context.Context, twitch.Import, string) error
}

func (s *Service) releaseFailedImport(ctx context.Context, api domain.FlowTerex, failure domain.FailedImport) {
	store, ok := s.store.(failureStore)
	aborter, canAbort := api.(importAborter)
	if !ok || !canAbort || failure.Reservation.ImportID == "" {
		return
	}
	var err error
	if fenced, ok := api.(interface {
		AbortFailedImport(context.Context, domain.FailedImport) error
	}); ok {
		err = fenced.AbortFailedImport(ctx, failure)
	} else {
		err = aborter.AbortImport(ctx, failure.Reservation, failure.Reason)
	}
	var remote *domain.Failure
	obsolete := errors.As(err, &remote) && (remote.HTTPStatus == 404 || remote.HTTPStatus == 409 && remote.Code != "twitch_reset")
	if err != nil && !obsolete {
		s.log.WarnContext(ctx, "collector preparation release unavailable", "clip_id", failure.ClipID, "import_id", failure.Reservation.ImportID, "reason", safeErrorMessage(err, "Не удалось освободить резерв подготовки"))
		return
	}
	if err = store.FailedImportSynced(ctx, failure); err != nil {
		s.log.WarnContext(ctx, "collector failure acknowledgement unavailable", errorFields(err)...)
		return
	}
	s.log.InfoContext(ctx, "collector failed import reconciled", "clip_id", failure.ClipID, "import_id", failure.Reservation.ImportID, "generation", failure.Reservation.UploadGeneration, "obsolete", obsolete)
}

func (s *Service) reconcileFailedImports(ctx context.Context, api domain.FlowTerex, epoch int64) {
	store, ok := s.store.(failureStore)
	if !ok {
		return
	}
	s.mu.Lock()
	if time.Since(s.lastFailureSyncAt) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastFailureSyncAt = time.Now()
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	failures, err := store.FailedImports(ctx, epoch)
	if err != nil {
		s.log.WarnContext(ctx, "collector failed imports unavailable", errorFields(err)...)
		return
	}
	for _, failure := range failures {
		if ctx.Err() != nil {
			return
		}
		s.releaseFailedImport(ctx, api, failure)
	}
}
