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
	limit int
}

func (a *retryAdmissionsAPI) Admissions(_ context.Context, q twitch.AdmissionRequest) (twitch.AdmissionResult, error) {
	a.calls++
	a.limit = q.Limit
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
	cfg.Settings.DailyDownloads = 100
	if _, err := s.queueAdmissions(context.Background(), cfg); err == nil {
		t.Fatal("missing failed allocation")
	}
	if api.limit != admissionBufferSize || cfg.Settings.DailyDownloads != 100 {
		t.Fatal("execution buffer changed the daily quota", api.limit, cfg.Settings.DailyDownloads)
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
	cfg.Settings.DailyDownloads = 3
	s.lastAdmissionsAt = time.Time{}
	if _, err := s.queueAdmissions(context.Background(), cfg); err != nil || api.limit != 3 {
		t.Fatal("buffer exceeded the configured daily quota", api.limit, err)
	}
	cfg.Settings.DailyDownloads = 0
	s.lastAdmissionsAt = time.Time{}
	if _, err := s.queueAdmissions(context.Background(), cfg); err != nil || api.limit != admissionBufferSize {
		t.Fatal("unlimited daily quota disabled or expanded the executable buffer", api.limit, err)
	}
}
