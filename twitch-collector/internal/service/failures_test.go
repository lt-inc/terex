package service

import (
	"context"
	"io"
	"log/slog"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"testing"
)

type durableFailureStore struct {
	domain.Store
	pending []domain.FailedImport
	acked   int
}

func (s *durableFailureStore) FailedImports(context.Context, int64) ([]domain.FailedImport, error) {
	return s.pending, nil
}
func (s *durableFailureStore) FailedImportSynced(context.Context, domain.FailedImport) error {
	s.acked++
	s.pending = nil
	return nil
}

type failingAbortAPI struct {
	domain.FlowTerex
	calls int
}

func (a *failingAbortAPI) AbortImport(_ context.Context, imp twitch.Import, _ string) error {
	a.calls++
	if imp.ImportID != "admitted-import" || imp.UploadGeneration != 0 {
		panic("lost admission identity before the first upload")
	}
	if a.calls == 1 {
		return &domain.Failure{Kind: "network", Retryable: true, Message: "lost abort response"}
	}
	return nil
}

func TestFailedAdmissionReleaseSurvivesRestart(t *testing.T) {
	store := &durableFailureStore{pending: []domain.FailedImport{{ClipID: "clip", Reservation: twitch.Import{ImportID: "admitted-import", Epoch: 1}, Reason: "source rejected before upload"}}}
	api := &failingAbortAPI{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	newService := func() *Service { return New(store, nil, nil, nil, logger) }
	newService().reconcileFailedImports(context.Background(), api, 1)
	if api.calls != 1 || store.acked != 0 {
		t.Fatal("lost response discarded the durable release", api.calls, store.acked)
	}
	newService().reconcileFailedImports(context.Background(), api, 1)
	if api.calls != 2 || store.acked != 1 {
		t.Fatal("restart did not retry the generation-zero release", api.calls, store.acked)
	}
	newService().reconcileFailedImports(context.Background(), api, 1)
	if api.calls != 2 {
		t.Fatal("acknowledged failure was released repeatedly")
	}
}
