package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"terex/proto/telemetry"
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
	gateLogs    map[string]gateLog
}

func New(store domain.Store, twitch domain.Twitch, terex domain.Terex, downloader domain.Downloader, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, twitch: twitch, terex: terex, downloader: downloader, log: logger, active: map[string]context.CancelCauseFunc{}, gateLogs: map[string]gateLog{}, ctx: context.Background()}
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
		s.scheduleOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) scheduleOnce(ctx context.Context) {
	cfg, err := s.terex.Settings(ctx)
	reason := settingsBlockReason(cfg, err, s.twitch.Ready())
	if reason != "" {
		if s.logGate(ctx, "scheduler", reason, err) {
			if e := s.store.SetLastCheck(ctx, time.Now().UTC(), reason); e != nil {
				s.log.WarnContext(ctx, "collector status could not be saved", append([]any{"stage", "scheduler"}, errorFields(e)...)...)
			}
		}
		return
	}
	s.logGate(ctx, "scheduler", "", nil)
	s.mu.Lock()
	due := time.Since(s.lastStarted) >= time.Duration(cfg.PollIntervalMinutes)*time.Minute
	s.mu.Unlock()
	if due {
		if err := s.Check(); err != nil && !errors.Is(err, domain.ErrConflict) && ctx.Err() == nil {
			s.log.WarnContext(ctx, "collector scheduled check could not start", errorFields(err)...)
		}
	}
}
func (s *Service) Check() error {
	if !s.twitch.Ready() {
		s.logGate(s.ctx, "check", "Twitch application credentials are not configured", nil)
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
	s.log.InfoContext(s.ctx, "collector check accepted")
	go s.check(s.ctx)
	return nil
}
func (s *Service) check(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	ctx, span := telemetry.Start(ctx, "collector.check")
	defer func() { s.mu.Lock(); s.checking = false; s.mu.Unlock() }()
	now := time.Now().UTC()
	message := ""
	channelsChecked, fetched, eligible, reserved := 0, 0, 0, 0
	outcome := "completed"
	s.log.InfoContext(ctx, "collector check started")
	defer func() {
		if message != "" {
			outcome = "blocked_or_failed"
		}
		var spanError error
		if message != "" {
			spanError = errors.New(message)
		}
		telemetry.End(span, spanError)
		s.log.InfoContext(ctx, "collector check finished", "status", outcome, "channels", channelsChecked, "fetched", fetched, "eligible", eligible, "reserved", reserved, "duration_ms", time.Since(now).Milliseconds(), "reason", message)
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		if err := s.store.SetLastCheck(saveCtx, now, message); err != nil {
			s.log.WarnContext(ctx, "collector check result could not be saved", errorFields(err)...)
		}
	}()
	cfg, err := s.terex.Settings(ctx)
	if err != nil {
		message = "Terex settings unavailable"
		s.log.WarnContext(ctx, message, errorFields(err)...)
		return
	}
	if reason := settingsBlockReason(cfg, nil, true); reason != "" {
		message = reason
		return
	}
	pending, err := s.terex.PendingCount(ctx)
	if err != nil {
		message = "Terex backlog unavailable"
		s.log.WarnContext(ctx, message, errorFields(err)...)
		return
	}
	if pending >= cfg.BacklogLimit {
		message = "Terex backlog limit reached"
		s.log.InfoContext(ctx, "collector check blocked", "reason", message, "pending", pending, "backlog_limit", cfg.BacklogLimit)
		return
	}
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		message = "Channel list unavailable: " + safeErrorMessage(err, "collector database request failed")
		s.log.WarnContext(ctx, message, errorFields(err)...)
		return
	}
	var candidates []domain.Clip
	s.log.InfoContext(ctx, "collector discovery settings", "check_every_minutes", cfg.PollIntervalMinutes, "window_hours", cfg.WindowHours, "min_views", cfg.MinViews, "min_age_minutes", cfg.MinAgeMinutes, "min_duration_seconds", cfg.MinDurationSec, "max_duration_seconds", cfg.MaxDurationSec, "daily_downloads", cfg.DailyDownloads, "per_channel_daily_downloads", cfg.PerChannelDailyDownloads)
	for _, ch := range channels {
		if ch.Paused || ch.Archived {
			continue
		}
		channelsChecked++
		s.log.InfoContext(ctx, "collector channel check started", "channel", ch.Login, "channel_id", ch.ID)
		clips, e := s.twitch.GetClips(ctx, ch, now.Add(-time.Duration(cfg.WindowHours)*time.Hour), now, cfg.First)
		channelError := ""
		if e != nil {
			channelError = safeErrorMessage(e, "Twitch channel metadata unavailable")
			message = "one or more channels could not be checked"
			fields := append([]any{"channel", ch.Login, "channel_id", ch.ID}, errorFields(e)...)
			s.log.WarnContext(ctx, "collector channel check failed", fields...)
		} else if e = s.store.UpsertClips(ctx, clips); e != nil {
			channelError = "Clip metadata storage unavailable: " + safeErrorMessage(e, "collector database request failed")
			message = channelError
			s.log.WarnContext(ctx, channelError, append([]any{"channel", ch.Login, "channel_id", ch.ID}, errorFields(e)...)...)
		} else {
			candidates = append(candidates, clips...)
			count := len(Select(clips, cfg, now))
			fetched += len(clips)
			eligible += count
			s.log.InfoContext(ctx, "collector channel check finished", "channel", ch.Login, "channel_id", ch.ID, "fetched", len(clips), "eligible", count, "filtered_or_overlap", len(clips)-count)
		}
		if e := s.store.ChannelChecked(ctx, ch.ID, now, channelError); e != nil {
			s.log.WarnContext(ctx, "collector channel check result could not be saved", append([]any{"channel", ch.Login, "channel_id", ch.ID}, errorFields(e)...)...)
		}
	}
	if channelsChecked == 0 {
		message = "no active Twitch channels; add or resume a monitored channel"
		return
	}
	// Refresh the global gate after metadata requests, which can take time.
	current, e := s.terex.Settings(ctx)
	if reason := settingsBlockReason(current, e, true); reason != "" {
		message = reason
		return
	}
	pending, e = s.terex.PendingCount(ctx)
	if e != nil || pending >= current.BacklogLimit {
		message = "Terex backlog limit reached"
		if e != nil {
			message = "Terex backlog unavailable"
			s.log.WarnContext(ctx, message, errorFields(e)...)
		}
		return
	}
	selected := Select(candidates, current, now)
	eligible = len(selected)
	if reserved, e = s.store.Reserve(ctx, selected, current, now); e != nil {
		message = "Could not queue clip downloads: " + safeErrorMessage(e, "collector database request failed") + ". Unqueued candidates remain discovered; the next check will retry."
		s.log.ErrorContext(ctx, "collector download reservation failed", append([]any{"stage", "reservation", "selected", len(selected), "reason", message}, errorFields(e)...)...)
		return
	}
	reason := ""
	if len(selected) == 0 {
		reason = "no clips match the discovery window, view, age and duration filters"
	} else if reserved == 0 {
		reason = "matching clips are already reserved, overlap an existing source or exceed today's limits"
	}
	s.log.InfoContext(ctx, "collector downloads queued", "selected", len(selected), "reserved", reserved, "reason", reason)
}
func (s *Service) Channels(ctx context.Context) ([]domain.Channel, error) {
	return s.store.ListChannels(ctx)
}
func (s *Service) AddChannel(ctx context.Context, input string) (domain.Channel, error) {
	ch, err := s.twitch.ResolveChannel(ctx, input)
	if err != nil {
		s.log.WarnContext(ctx, "collector channel could not be resolved", errorFields(err)...)
		return domain.Channel{}, err
	}
	ch, err = s.store.CreateChannel(ctx, ch)
	if err == nil {
		s.log.InfoContext(ctx, "collector channel added", "channel", ch.Login, "channel_id", ch.ID)
	}
	return ch, err
}
func (s *Service) SetChannel(ctx context.Context, id string, paused *bool, archive bool) (domain.Channel, error) {
	ch, err := s.store.SetChannel(ctx, id, paused, archive)
	if err == nil {
		s.log.InfoContext(ctx, "collector channel updated", "channel", ch.Login, "channel_id", ch.ID, "paused", ch.Paused, "archived", ch.Archived)
	}
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
	out.BlockReason = settingsBlockReason(cfg, e, out.CredentialsReady)
	if e != nil {
		out.LastError = out.BlockReason
	} else if out.BlockReason == "" {
		pending, e := s.terex.PendingCount(ctx)
		if e != nil {
			out.BlockReason = "Terex backlog unavailable"
		} else if pending >= cfg.BacklogLimit {
			out.BlockReason = "Terex backlog limit reached; review or reject pending sources"
		}
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
			if reason := settingsBlockReason(cfg, err, s.twitch.Ready()); reason != "" {
				s.logGate(ctx, "worker", reason, err)
				continue
			}
			pending, err := s.terex.PendingCount(ctx)
			if err != nil || pending >= cfg.BacklogLimit {
				reason := "Terex backlog limit reached"
				if err != nil {
					reason = "Terex backlog unavailable"
				}
				s.logGate(ctx, "worker", reason, err)
				continue
			}
			s.logGate(ctx, "worker", "", nil)
			task, err := s.store.Claim(ctx, 30*time.Second)
			if err != nil {
				s.logGate(ctx, "task_claim", "collector task claim failed", err)
				continue
			}
			s.logGate(ctx, "task_claim", "", nil)
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
	ctx, span := telemetry.Start(ctx, "collector.process")
	defer span.End()
	log := s.log.With("clip_id", task.Clip.ID, "channel_id", task.Clip.ChannelID, "channel", task.Clip.BroadcasterName, "attempt", task.Attempt)
	stage := "guard"
	started := time.Now()
	log.InfoContext(ctx, "collector task started")
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
	deferTask := func(reason string) {
		dbCtx, end := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer end()
		retry := time.Now().Add(time.Minute)
		if err := s.store.Defer(dbCtx, task, retry); err != nil {
			log.WarnContext(ctx, "collector task deferral could not be saved", append([]any{"stage", stage}, errorFields(err)...)...)
			return
		}
		log.InfoContext(ctx, "collector task deferred", "stage", stage, "reason", reason, "retry_at", retry.UTC())
	}
	failure := func(err error) {
		cause := context.Cause(ctx)
		if errors.Is(cause, domain.ErrPaused) || errors.Is(err, domain.ErrPaused) || errors.Is(cause, context.Canceled) {
			deferTask("collector or channel paused, target account changed, or service stopping")
			return
		}
		if errors.Is(cause, domain.ErrLeaseLost) || errors.Is(err, domain.ErrLeaseLost) {
			log.WarnContext(ctx, "collector task stopped after lease loss", "stage", stage)
			return
		}
		var f *domain.Failure
		if !errors.As(err, &f) {
			f = &domain.Failure{Kind: "internal", Message: "clip processing failed"}
		}
		dbCtx, end := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer end()
		retry := RetryAt(task.Attempt, f, time.Now())
		fields := append([]any{"stage", stage, "retry_scheduled", retry != nil, "duration_ms", time.Since(started).Milliseconds()}, errorFields(f)...)
		if retry != nil {
			fields = append(fields, "retry_at", retry.UTC())
		}
		if retry == nil {
			log.ErrorContext(ctx, "collector task failed", fields...)
		} else {
			log.WarnContext(ctx, "collector task failed", fields...)
		}
		span.RecordError(errors.New(f.Message))
		if err := s.store.Fail(dbCtx, task, f.Kind, f.Message, retry); err != nil {
			log.WarnContext(ctx, "collector task failure could not be saved", append([]any{"stage", stage}, errorFields(err)...)...)
		}
	}
	if err := s.guard(ctx, task); err != nil {
		failure(err)
		return
	}
	stage = "download"
	log.InfoContext(ctx, "collector download started", "duration_seconds", task.Clip.Duration)
	downloadCtx, downloadSpan := telemetry.Start(ctx, "collector.download")
	file, err := s.downloader.Download(downloadCtx, task.Clip.ID, task.Clip.Duration)
	telemetry.End(downloadSpan, sanitizedError(err))
	if err != nil {
		failure(err)
		return
	}
	defer file.Cleanup()
	log.InfoContext(ctx, "collector download completed", "size_bytes", file.Size, "duration_seconds", file.Duration, "width", file.Width, "height", file.Height, "has_audio", file.HasAudio)
	stage = "deduplicate"
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
		deferTask("Terex settings unavailable or collector paused")
		return
	}
	if current.TargetAccountID != cfg.TargetAccountID {
		deferTask("target YouTube account changed")
		return
	}
	pending, err := s.terex.PendingCount(ctx)
	if err != nil || pending >= current.BacklogLimit {
		deferTask("Terex backlog unavailable or limit reached")
		return
	}
	stage = "import"
	log.InfoContext(ctx, "collector import started", "target_account_id", current.TargetAccountID)
	importCtx, importSpan := telemetry.Start(ctx, "collector.import")
	importID, err := s.terex.Import(importCtx, task.Clip, *file, current.TargetAccountID, func(c context.Context) error { return s.guardTarget(c, task, current.TargetAccountID) })
	telemetry.End(importSpan, sanitizedError(err))
	if err != nil {
		failure(err)
		return
	}
	stage = "complete"
	if err = s.guard(ctx, task); err != nil {
		failure(err)
		return
	}
	if err = s.store.Complete(ctx, task, importID, file.SHA256); err != nil {
		log.WarnContext(ctx, "collector completion not committed; idempotent import will reconcile on retry", append([]any{"import_id", importID}, errorFields(err)...)...)
		return
	}
	log.InfoContext(ctx, "collector import completed", "import_id", importID, "duration_ms", time.Since(started).Milliseconds())
}
