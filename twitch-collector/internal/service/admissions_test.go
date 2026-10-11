package service

import (
	"context"
	"testing"
	"time"

	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

type retryAdmissionsAPI struct {
	domain.Terex
	calls int
	fail  bool
}

func (a *retryAdmissionsAPI) Admissions(context.Context, twitch.AdmissionRequest) (twitch.AdmissionResult, error) {
	a.calls++
	if a.fail {
		return twitch.AdmissionResult{}, context.DeadlineExceeded
	}
	return twitch.AdmissionResult{Mode: "enforce", Grants: []twitch.DownloadGrant{{ClipID: "clip", ImportID: "import", Epoch: 1}}}, nil
}

type retryAdmissionsStore struct {
	domain.Store
	queued int
}

func (s *retryAdmissionsStore) QueueAdmissions(_ context.Context, grants []twitch.DownloadGrant, _ time.Time) (int, error) {
	s.queued += len(grants)
	return len(grants), nil
}

func TestAdmissionsTimeoutBacksOffAndRecoveryQueuesOnlyOnce(t *testing.T) {
	api, store := &retryAdmissionsAPI{fail: true}, &retryAdmissionsStore{}
	s := New(store, nil, api, nil, nil)
	cfg := twitch.CollectorConfig{Epoch: 1, Settings: settings()}
	if _, err := s.queueAdmissions(context.Background(), cfg); err == nil {
		t.Fatal("missing failed allocation")
	}
	if _, err := s.queueAdmissions(context.Background(), cfg); err != nil || api.calls != 1 || store.queued != 0 {
		t.Fatal("failed allocation was immediately repeated", api.calls, err)
	}
	api.fail = false
	s.admissionsRetryAt = time.Now().Add(-time.Second)
	if n, err := s.queueAdmissions(context.Background(), cfg); err != nil || n != 1 {
		t.Fatal("recovered grant was not queued", n, err)
	}
	if _, err := s.queueAdmissions(context.Background(), cfg); err != nil || api.calls != 2 || store.queued != 1 {
		t.Fatal("successful allocation bypassed normal polling interval", api.calls, store.queued, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.queueAdmissions(canceled, cfg); err == nil || api.calls != 2 {
		t.Fatal("expired pass sent an allocation request", api.calls, err)
	}
}
