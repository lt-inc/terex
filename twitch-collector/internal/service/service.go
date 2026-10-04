package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"terex/twitch-collector/internal/domain"
)

type Service struct {
	store       domain.Store
	twitch      domain.Twitch
	terex       domain.Terex
	downloader  domain.Downloader
	log         *slog.Logger
	ctx         context.Context
	mu          sync.Mutex
	checking    bool
	lastStarted time.Time
	active      map[string]context.CancelCauseFunc
}

func New(store domain.Store, twitch domain.Twitch, terex domain.Terex, downloader domain.Downloader, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, twitch: twitch, terex: terex, downloader: downloader, log: logger, active: map[string]context.CancelCauseFunc{}, ctx: context.Background()}
}
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	go s.schedule(ctx)
	go s.worker(ctx)
}
func (s *Service) schedule(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		cfg, err := s.terex.Settings(ctx)
		if err == nil && cfg.Enabled && s.twitch.Ready() {
			s.mu.Lock()
			due := time.Since(s.lastStarted) >= time.Duration(cfg.PollIntervalMinutes)*time.Minute
			s.mu.Unlock()
			if due {
				_ = s.Check()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) Check() error {
	if !s.twitch.Ready() {
		return errors.New("Twitch application credentials are not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checking {
		return domain.ErrConflict
	}
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	s.checking = true
	s.lastStarted = time.Now()
	go s.check(s.ctx)
	return nil
}
func (s *Service) check(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	defer func() { s.mu.Lock(); s.checking = false; s.mu.Unlock() }()
	now := time.Now().UTC()
	message := ""
	defer func() {
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		_ = s.store.SetLastCheck(saveCtx, now, message)
	}()
	cfg, err := s.terex.Settings(ctx)
	if err != nil {
		message = "Terex settings unavailable"
		return
	}
	if !cfg.Enabled {
		message = "collector is paused"
		return
	}
	pending, err := s.terex.PendingCount(ctx)
	if err != nil {
		message = "Terex backlog unavailable"
		return
	}
	if pending >= cfg.BacklogLimit {
		message = "Terex backlog limit reached"
		return
	}
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		message = "channel list unavailable"
		return
	}
	var candidates []domain.Clip
	for _, ch := range channels {
		if ch.Paused || ch.Archived {
			continue
		}
		clips, e := s.twitch.GetClips(ctx, ch, now.Add(-time.Duration(cfg.WindowHours)*time.Hour), now, cfg.First)
		channelError := ""
		if e != nil {
			channelError = e.Error()
			message = "one or more channels could not be checked"
		} else if e = s.store.UpsertClips(ctx, clips); e != nil {
			channelError = "clip metadata storage unavailable"
			message = channelError
		} else {
			candidates = append(candidates, clips...)
		}
		_ = s.store.ChannelChecked(ctx, ch.ID, now, channelError)
	}
	// Refresh the global gate after metadata requests, which can take time.
	current, e := s.terex.Settings(ctx)
	if e != nil || !current.Enabled {
		return
	}
	pending, e = s.terex.PendingCount(ctx)
	if e != nil || pending >= current.BacklogLimit {
		return
	}
	if _, e = s.store.Reserve(ctx, Select(candidates, current, now), current, now); e != nil {
		message = "clip reservations unavailable"
	}
}
func (s *Service) Channels(ctx context.Context) ([]domain.Channel, error) {
	return s.store.ListChannels(ctx)
}
func (s *Service) AddChannel(ctx context.Context, input string) (domain.Channel, error) {
	ch, err := s.twitch.ResolveChannel(ctx, input)
	if err != nil {
		return domain.Channel{}, err
	}
	return s.store.CreateChannel(ctx, ch)
}
func (s *Service) SetChannel(ctx context.Context, id string, paused *bool, archive bool) (domain.Channel, error) {
	ch, err := s.store.SetChannel(ctx, id, paused, archive)
	if err == nil && (ch.Paused || ch.Archived) {
		s.mu.Lock()
		if cancel := s.active[id]; cancel != nil {
			cancel(domain.ErrPaused)
		}
		s.mu.Unlock()
	}
	return ch, err
}
func (s *Service) Clips(ctx context.Context, limit int, state string) ([]domain.Clip, error) {
	return s.store.ListClips(ctx, limit, state)
}
func (s *Service) Status(ctx context.Context) (domain.Status, error) {
	out, err := s.store.Status(ctx)
	if err != nil {
		return out, err
	}
	s.mu.Lock()
	out.Running = s.checking
	s.mu.Unlock()
	out.CredentialsReady = s.twitch.Ready()
	cfg, e := s.terex.Settings(ctx)
	out.Paused = e != nil || !cfg.Enabled
	if e != nil {
		out.LastError = "Terex settings unavailable"
	}
	return out, nil
}
func (s *Service) worker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg, err := s.terex.Settings(ctx)
			if err != nil || !cfg.Enabled || !s.twitch.Ready() {
				continue
			}
			pending, err := s.terex.PendingCount(ctx)
			if err != nil || pending >= cfg.BacklogLimit {
				continue
			}
			task, err := s.store.Claim(ctx, 30*time.Second)
			if err != nil {
				s.log.Warn("collector task claim failed")
				continue
			}
			if task != nil {
				s.process(ctx, *task, cfg)
			}
		}
	}
}
func (s *Service) guard(ctx context.Context, task domain.Task) error {
	return s.guardTarget(ctx, task, 0)
}
func (s *Service) guardTarget(ctx context.Context, task domain.Task, target int64) error {
	cfg, err := s.terex.Settings(ctx)
	if err != nil {
		return domain.ErrPaused
	}
	if !cfg.Enabled {
		return domain.ErrPaused
	}
	if target > 0 && cfg.TargetAccountID != target {
		return domain.ErrPaused
	}
	ch, err := s.store.GetChannel(ctx, task.Clip.ChannelID)
	if err != nil {
		return err
	}
	if ch.Paused || ch.Archived {
		return domain.ErrPaused
	}
	return s.store.ValidLease(ctx, task)
}
func (s *Service) process(parent context.Context, task domain.Task, cfg domain.Settings) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	s.mu.Lock()
	s.active[task.Clip.ChannelID] = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.active, task.Clip.ChannelID); s.mu.Unlock() }()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.guardTarget(ctx, task, cfg.TargetAccountID); err != nil {
					cancel(err)
					return
				}
				if err := s.store.Renew(ctx, task, 30*time.Second); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	deferTask := func() {
		dbCtx, end := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer end()
		_ = s.store.Defer(dbCtx, task, time.Now().Add(time.Minute))
	}
	failure := func(err error) {
		cause := context.Cause(ctx)
		if errors.Is(cause, domain.ErrPaused) || errors.Is(err, domain.ErrPaused) || errors.Is(cause, context.Canceled) {
			deferTask()
			return
		}
		if errors.Is(cause, domain.ErrLeaseLost) || errors.Is(err, domain.ErrLeaseLost) {
			return
		}
		var f *domain.Failure
		if !errors.As(err, &f) {
			f = &domain.Failure{Kind: "internal", Message: "clip processing failed"}
		}
		dbCtx, end := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer end()
		_ = s.store.Fail(dbCtx, task, f.Kind, f.Message, RetryAt(task.Attempt, f, time.Now()))
	}
	if err := s.guard(ctx, task); err != nil {
		failure(err)
		return
	}
	file, err := s.downloader.Download(ctx, task.Clip.ID, task.Clip.Duration)
	if err != nil {
		failure(err)
		return
	}
	defer file.Cleanup()
	if err = s.guard(ctx, task); err != nil {
		failure(err)
		return
	}
	duplicate, err := s.store.HasSHA256(ctx, file.SHA256, task.Clip.ID)
	if err != nil {
		failure(err)
		return
	}
	if duplicate {
		failure(&domain.Failure{Kind: "duplicate", Message: "identical source video already imported"})
		return
	}
	current, err := s.terex.Settings(ctx)
	if err != nil || !current.Enabled {
		deferTask()
		return
	}
	if current.TargetAccountID != cfg.TargetAccountID {
		deferTask()
		return
	}
	pending, err := s.terex.PendingCount(ctx)
	if err != nil || pending >= current.BacklogLimit {
		deferTask()
		return
	}
	importID, err := s.terex.Import(ctx, task.Clip, *file, current.TargetAccountID, func(c context.Context) error { return s.guardTarget(c, task, current.TargetAccountID) })
	if err != nil {
		failure(err)
		return
	}
	if err = s.guard(ctx, task); err != nil {
		failure(err)
		return
	}
	if err = s.store.Complete(ctx, task, importID, file.SHA256); err != nil {
		s.log.Warn("collector completion not committed; idempotent import will reconcile on retry")
	}
}
