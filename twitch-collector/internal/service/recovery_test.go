package service

import (
	"context"
	"strings"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"testing"
	"time"
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
	notReady              bool
	disabled              bool
	legacyReady           bool
	gets, completes, puts int
}

func (a *uploadedAPI) CollectorConfig(context.Context) (twitch.CollectorConfig, error) {
	cfg := settings()
	if a.disabled {
		cfg.Enabled = false
	}
	return twitch.CollectorConfig{Epoch: 1, Settings: cfg}, nil
}
func (a *uploadedAPI) ImportStatus(context.Context, string) (twitch.Import, error) {
	a.gets++
	v := twitch.Import{Epoch: 1, ImportID: "import", UploadGeneration: 3}
	if a.ready {
		v.MediaState = "ready"
	}
	if a.legacyReady {
		v.State, v.MediaState = "ready", "upload_pending"
	}
	return v, nil
}
func (a *uploadedAPI) CompleteImport(context.Context, twitch.Import, domain.UploadedMedia) (twitch.Import, error) {
	a.completes++
	if a.notReady {
		return twitch.Import{Epoch: 1, ImportID: "import", UploadGeneration: 3, State: "upload_pending"}, nil
	}
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

func TestCompletionDoesNotAccept202PendingAndDrainsPausedFlow(t *testing.T) {
	store := &uploadedStore{fakeStore: &fakeStore{paused: true}}
	api := &uploadedAPI{notReady: true}
	s := New(store, nil, api, fakeDownloader{fn: func(context.Context) (*domain.Download, error) { t.Fatal("completion redownloaded"); return nil, nil }}, nil)
	task := task()
	task.Completion = true
	task.Epoch = 1
	task.FlowID = "paused-flow"
	task.Reservation = twitch.Import{Epoch: 1, ImportID: "import", UploadGeneration: 3}
	task.Uploaded = &domain.UploadedMedia{SHA256: strings.Repeat("a", 64), SizeBytes: 100, Phase: "uploading"}
	s.processFlow(context.Background(), task, api, store)
	if store.completed != 0 || store.deferred != 0 || store.failed != 1 || store.retry == nil || api.completes != 1 || api.puts != 0 {
		t.Fatal("pending response accepted or paused completion blocked", store.completed, store.deferred, store.failed, api.completes)
	}
	// A logical source's ready flag cannot override quarantined media. Old
	// generation-only tasks poll safely without redownloading or guessing SHA.
	api.legacyReady = true
	task.Uploaded = nil
	task.Attempt = 20
	s.processFlow(context.Background(), task, api, store)
	if store.completed != 0 || store.failed != 2 || store.retry == nil || api.completes != 1 {
		t.Fatal("generation-only task failed permanently or accepted logical readiness", store.completed, store.failed, store.retry, api.completes)
	}
	api.disabled = true
	if _, err := s.fetchCollectorConfig(context.Background(), api); err != nil {
		t.Fatal(err)
	}
	s.processFlow(context.Background(), task, api, store)
	if store.deferred != 1 || api.gets != 2 {
		t.Fatal("global stop allowed completion to continue", store.deferred, api.gets)
	}
	now := time.Now()
	for index, delay := range []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute} {
		if retry := CompletionRetryAt(index+1, &domain.Failure{Retryable: true}, now); retry == nil || retry.Sub(now) != delay {
			t.Fatal("verification retry spent download budget or used wrong delay", index, retry)
		}
	}
	if retry := CompletionRetryAt(999, &domain.Failure{Retryable: true}, now); retry == nil || retry.Sub(now) != 5*time.Minute {
		t.Fatal("long-running completion exhausted retry budget", retry)
	}
	if retry := CompletionRetryAt(1, &domain.Failure{Retryable: true, Code: "invalid_media"}, now); retry != nil {
		t.Fatal("confirmed corruption retried", retry)
	}
	s.recordPressure(twitch.PressureResult{Epoch: 1, PendingPhysical: 40, BacklogLimit: 40}, nil)
	if err := s.downloadPressure(twitch.CollectorConfig{Epoch: 1}); err == nil {
		t.Fatal("full backlog allowed new downloads")
	}
	s.recordPressure(twitch.PressureResult{Epoch: 1, BacklogLimit: 40, Storage: twitch.StoragePressure{Required: true, State: "ok", ObservedAt: now.Add(-4 * time.Minute)}}, nil)
	if err := s.downloadPressure(twitch.CollectorConfig{Epoch: 1}); err == nil {
		t.Fatal("stale storage status allowed new downloads")
	}
}

type resetStore struct {
	*fakeStore
	resets int
}

func (s *resetStore) ResetTwitchPool(context.Context, int64) error { s.resets++; return nil }

type resetAPI struct {
	domain.Terex
	acks int
}

func (a *resetAPI) AcknowledgeReset(context.Context, twitch.ResetCollectorAck) error {
	a.acks++
	return nil
}

func TestResetAcknowledgesOnlyAfterBothWorkerReleases(t *testing.T) {
	store := &resetStore{fakeStore: &fakeStore{}}
	api := &resetAPI{}
	s := New(store, nil, api, nil, nil)
	s.resetEpoch = 1
	first, releaseFirst, err := s.beginResetWorker(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	second, releaseSecond, err := s.beginResetWorker(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	cfg := twitch.CollectorConfig{Epoch: 2, Maintenance: &twitch.ResetMaintenance{ID: "reset", Epoch: 2}}
	if skip, err := s.handleReset(context.Background(), cfg); err != nil || !skip {
		t.Fatal(skip, err)
	}
	if first.Err() == nil || second.Err() == nil || store.resets != 0 || api.acks != 0 {
		t.Fatal("reset failed to cancel all workers or acknowledged before drain")
	}
	releaseFirst()
	if _, err = s.handleReset(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if store.resets != 0 || api.acks != 0 {
		t.Fatal("first release hid second worker")
	}
	releaseSecond()
	if _, err = s.handleReset(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if store.resets != 1 || api.acks != 1 || len(s.active) != 0 {
		t.Fatal("drained reset not acknowledged", store.resets, api.acks)
	}
}
