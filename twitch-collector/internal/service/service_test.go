package service

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"testing"
	"time"
)

type fakeStore struct {
	domain.Store
	mu          sync.Mutex
	paused      bool
	invalid     bool
	deferred    int
	failed      int
	retry       *time.Time
	completed   int
	lastChecks  int
	lastMessage string
}

func (f *fakeStore) GetChannel(context.Context, string) (domain.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return domain.Channel{ID: "channel", Paused: f.paused}, nil
}
func (f *fakeStore) SetChannel(_ context.Context, _ string, paused *bool, archive bool) (domain.Channel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = archive || (paused != nil && *paused)
	return domain.Channel{ID: "channel", Paused: f.paused}, nil
}
func (f *fakeStore) ValidLease(context.Context, domain.Task) error {
	if f.invalid {
		return domain.ErrLeaseLost
	}
	return nil
}
func (f *fakeStore) Renew(context.Context, domain.Task, time.Duration) error { return nil }
func (f *fakeStore) Defer(context.Context, domain.Task, time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deferred++
	return nil
}
func (f *fakeStore) Fail(_ context.Context, _ domain.Task, _, _ string, retry *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed++
	f.retry = retry
	return nil
}
func (f *fakeStore) HasSHA256(context.Context, string, string) (bool, error) { return false, nil }
func (f *fakeStore) Complete(context.Context, domain.Task, string, string) error {
	f.completed++
	return nil
}

type fakeTerex struct {
	domain.Terex
	mu       sync.Mutex
	settings twitch.Settings
	imports  int
	pending  int
	err      error
}

func (f *fakeTerex) Settings(context.Context) (domain.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.settings, f.err
}
func (f *fakeTerex) PendingCount(context.Context) (int, error) { return f.pending, nil }
func (f *fakeTerex) Import(context.Context, domain.Clip, domain.Download, int64, func(context.Context) error) (string, error) {
	f.imports++
	return "import", nil
}

type fakeDownloader struct {
	fn func(context.Context) (*domain.Download, error)
}

func (f fakeDownloader) Download(ctx context.Context, _ string, _ float64) (*domain.Download, error) {
	return f.fn(ctx)
}
func settings() twitch.Settings {
	s := twitch.DefaultSettings()
	s.Enabled = true
	s.TargetAccountID = 1
	return s
}
func task() domain.Task {
	return domain.Task{Clip: domain.Clip{ID: "Safe", ChannelID: "channel", Duration: 20}, Token: "fence", Attempt: 1}
}
func TestChannelPauseCancelsProcessWithoutConsumingAttempt(t *testing.T) {
	store := &fakeStore{}
	backend := &fakeTerex{settings: settings()}
	started := make(chan struct{})
	downloader := fakeDownloader{fn: func(ctx context.Context) (*domain.Download, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	svc := New(store, nil, backend, downloader, nil)
	done := make(chan struct{})
	go func() { svc.process(context.Background(), task(), settings()); close(done) }()
	<-started
	paused := true
	if _, err := svc.SetChannel(context.Background(), "channel", &paused, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pause did not cancel active download")
	}
	if store.deferred != 1 || store.failed != 0 || backend.imports != 0 {
		t.Fatalf("deferred=%d failed=%d imported=%d", store.deferred, store.failed, backend.imports)
	}
}
func TestStaleLeaseNeverDownloadsOrWritesFailure(t *testing.T) {
	store := &fakeStore{invalid: true}
	backend := &fakeTerex{settings: settings()}
	svc := New(store, nil, backend, fakeDownloader{fn: func(context.Context) (*domain.Download, error) { t.Fatal("stale lease downloaded"); return nil, nil }}, nil)
	svc.process(context.Background(), task(), settings())
	if store.failed != 0 || store.deferred != 0 || backend.imports != 0 {
		t.Fatal("stale worker changed state")
	}
}
func TestNetworkFailureSchedulesOnlyAllowedRetry(t *testing.T) {
	store := &fakeStore{}
	backend := &fakeTerex{settings: settings()}
	svc := New(store, nil, backend, fakeDownloader{fn: func(context.Context) (*domain.Download, error) {
		return nil, &domain.Failure{Kind: "network", Message: "network unavailable", Retryable: true}
	}}, nil)
	svc.process(context.Background(), task(), settings())
	if store.failed != 1 || store.retry == nil || time.Until(*store.retry) < 4*time.Minute || backend.imports != 0 {
		t.Fatal("network retry missing")
	}
	store.failed = 0
	store.retry = nil
	last := task()
	last.Attempt = 3
	svc.process(context.Background(), last, settings())
	if store.failed != 1 || store.retry != nil {
		t.Fatal("retry budget exceeded")
	}
}

func (f *fakeStore) SetLastCheck(_ context.Context, _ time.Time, message string) error {
	f.lastChecks++
	f.lastMessage = message
	return nil
}

type fakeTwitch struct {
	domain.Twitch
	ready bool
}

func (f fakeTwitch) Ready() bool { return f.ready }

func TestSchedulerExplainsBlockedCollectionWithoutPollingLogSpam(t *testing.T) {
	for _, test := range []struct {
		name  string
		cfg   twitch.Settings
		ready bool
		err   error
		want  string
	}{
		{name: "missing account", cfg: twitch.DefaultSettings(), ready: true, want: "Choose a connected YouTube account"},
		{name: "paused", cfg: func() twitch.Settings { cfg := settings(); cfg.Enabled = false; return cfg }(), ready: true, want: "Collector is paused"},
		{name: "missing Twitch keys", cfg: settings(), want: "Twitch application credentials"},
		{name: "backend authorization", cfg: settings(), ready: true, err: &domain.Failure{Kind: "import", Message: "Terex request failed (HTTP 401)", HTTPStatus: 401}, want: "HTTP 401"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			store := &fakeStore{}
			svc := New(store, fakeTwitch{ready: test.ready}, &fakeTerex{settings: test.cfg, err: test.err}, nil, slog.New(slog.NewJSONHandler(&output, nil)))
			svc.scheduleOnce(context.Background())
			svc.scheduleOnce(context.Background())
			if store.lastChecks != 1 || !strings.Contains(store.lastMessage, test.want) || strings.Count(output.String(), "collector waiting") != 1 {
				t.Fatalf("skip not explained or repeated: checks=%d message=%q logs=%s", store.lastChecks, store.lastMessage, output.String())
			}
		})
	}
}
func TestBacklogAndGlobalPauseBlockDiscovery(t *testing.T) {
	for _, paused := range []bool{false, true} {
		store := &fakeStore{}
		cfg := settings()
		cfg.Enabled = !paused
		backend := &fakeTerex{settings: cfg, pending: 40}
		svc := New(store, nil, backend, nil, nil)
		// Unimplemented calls panic if discovery reaches metadata or reservations.
		svc.check(context.Background())
		if store.lastChecks != 1 {
			t.Fatal("gate did not record a check result")
		}
	}
}
func TestGlobalPauseCancelsAlreadyRunningDownload(t *testing.T) {
	store := &fakeStore{}
	backend := &fakeTerex{settings: settings()}
	started := make(chan struct{})
	svc := New(store, nil, backend, fakeDownloader{fn: func(ctx context.Context) (*domain.Download, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}, nil)
	done := make(chan struct{})
	go func() { svc.process(context.Background(), task(), settings()); close(done) }()
	<-started
	backend.mu.Lock()
	backend.settings.Enabled = false
	backend.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("global pause failed to cancel download")
	}
	if store.deferred != 1 || backend.imports != 0 {
		t.Fatal("paused download imported or consumed attempt")
	}
}
