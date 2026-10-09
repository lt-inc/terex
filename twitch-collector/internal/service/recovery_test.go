package service

import (
	"context"
	"strings"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"testing"
)

type uploadedStore struct {
	*fakeStore
	domain.FlowStore
}

func (s *uploadedStore) SyncFlows(context.Context, []twitch.Flow) error          { return nil }
func (s *uploadedStore) HasActiveConsumer(context.Context, string) (bool, error) { return true, nil }

type uploadedAPI struct {
	domain.Terex
	domain.FlowTerex
	ready                 bool
	gets, completes, puts int
}

func (a *uploadedAPI) CollectorConfig(context.Context) (twitch.CollectorConfig, error) {
	return twitch.CollectorConfig{Epoch: 1, Settings: settings()}, nil
}
func (a *uploadedAPI) ImportStatus(context.Context, string) (twitch.Import, error) {
	a.gets++
	v := twitch.Import{Epoch: 1, ImportID: "import", UploadGeneration: 3}
	if a.ready {
		v.MediaState = "ready"
	}
	return v, nil
}
func (a *uploadedAPI) CompleteImport(context.Context, twitch.Import, domain.UploadedMedia) (twitch.Import, error) {
	a.completes++
	return twitch.Import{Epoch: 1, ImportID: "import", UploadGeneration: 3, MediaState: "ready"}, nil
}
func (a *uploadedAPI) PutImport(context.Context, twitch.Import, domain.Download, func(context.Context) error) error {
	a.puts++
	return nil
}

func TestUploadedRetryReconcilesWithoutDownloadingOrPuttingAgain(t *testing.T) {
	for _, ready := range []bool{false, true} {
		store := &uploadedStore{fakeStore: &fakeStore{}}
		api := &uploadedAPI{ready: ready}
		downloads := 0
		s := New(store, nil, api, fakeDownloader{fn: func(context.Context) (*domain.Download, error) {
			downloads++
			t.Fatal("uploaded source downloaded again")
			return nil, nil
		}}, nil)
		task := task()
		task.Epoch = 1
		task.FlowID = "flow"
		task.Reservation = twitch.Import{Epoch: 1, ImportID: "import", UploadGeneration: 3}
		task.Uploaded = &domain.UploadedMedia{SHA256: strings.Repeat("a", 64), SizeBytes: 100, Phase: "uploaded"}
		s.processFlow(context.Background(), task, api, store)
		wantComplete := 1
		if ready {
			wantComplete = 0
		}
		if store.completed != 1 || store.failed != 0 || api.gets != 1 || api.completes != wantComplete || api.puts != 0 || downloads != 0 {
			t.Fatal("upload retry lost its durable phase", ready, store.completed, store.failed, api.gets, api.completes, api.puts, downloads)
		}
	}
}
