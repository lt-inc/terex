package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"terex/proto/telemetry"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

// Keep coordinator transactions short without changing Helix page sizes.
const observationBatchSize = 20

func flowBlockReason(c twitch.CollectorConfig, err error, ready bool) string {
	if err != nil {
		return "Не удалось получить настройки Terex: " + safeErrorMessage(err, "проверьте связь и служебный токен")
	}
	if c.Maintenance != nil {
		return "Полный сброс Twitch: новые задания временно остановлены"
	}
	if !ready {
		return "Не заданы TWITCH_CLIENT_ID и TWITCH_CLIENT_SECRET"
	}
	if !c.Settings.Enabled {
		return "Коллектор остановлен в настройках сервиса"
	}
	for _, f := range c.Flows {
		if f.Enabled {
			return ""
		}
	}
	return "Нет активных потоков с подтверждённым YouTube-каналом"
}

func syncFlowConfig(ctx context.Context, store domain.FlowStore, cfg twitch.CollectorConfig) error {
	if err := store.SyncFlows(ctx, cfg.Flows); err != nil {
		return err
	}
	if exclusions, ok := store.(interface {
		SyncExclusions(context.Context, twitch.CollectorConfig) error
	}); ok {
		return exclusions.SyncExclusions(ctx, cfg)
	}
	return nil
}

func (s *Service) scheduleFlows(ctx context.Context) {
	api := s.terex.(domain.FlowTerex)
	cfg, err := s.collectorConfig(ctx, api)
	if err == nil {
		if skip, e := s.handleReset(ctx, cfg); skip || e != nil {
			return
		}
	}
	if reason := flowBlockReason(cfg, err, s.twitch.Ready()); reason != "" {
		s.logGate(ctx, "scheduler", reason, err)
		return
	}
	s.logGate(ctx, "scheduler", "", nil)
	s.mu.Lock()
	due := false
	for _, f := range cfg.Flows {
		interval := f.PollIntervalMinutes
		if interval < 1 {
			interval = cfg.Settings.PollIntervalMinutes
		}
		if f.Enabled && time.Since(s.flowStarted[f.ID]) >= time.Duration(interval)*time.Minute {
			due = true
			break
		}
	}
	s.mu.Unlock()
	if due {
		if err := s.startFlowCheck("", false); err != nil && !errors.Is(err, domain.ErrConflict) {
			s.log.WarnContext(ctx, "collector check could not start", errorFields(err)...)
		}
	}
}
func (s *Service) CheckFlow(id string) error { return s.startFlowCheck(id, true) }
func (s *Service) startFlowCheck(id string, force bool) error {
	if !s.twitch.Ready() {
		return errors.New("Twitch application credentials are not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resetting {
		return domain.ErrPaused
	}
	if s.checking {
		return domain.ErrConflict
	}
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	s.checking = true
	go s.checkFlows(s.ctx, id, force)
	return nil
}
func discoverySettings(g twitch.Settings, f twitch.Flow) twitch.Settings {
	g.PollIntervalMinutes = f.PollIntervalMinutes
	g.WindowHours = f.WindowHours
	g.MinViews = f.MinViews
	g.MinAgeMinutes = f.MinAgeMinutes
	g.MinDurationSec = f.MinDurationSec
	g.MaxDurationSec = f.MaxDurationSec
	return g
}

// Terex rejects a whole observation batch with HTTP 400 when one source is
// outside its duration limits, so such clips are never sent.

// observableSources converts clips into observation sources, leaving out the
// ones Terex would refuse so they cannot fail the batch they travel in.
func observableSources(clips []domain.Clip) []twitch.Source {
	sources := make([]twitch.Source, 0, len(clips))
	for _, c := range clips {
		if c.Duration <= 0 || c.Duration > twitch.MaxClipDurationSec {
			continue
		}
		sources = append(sources, clipSource(c))
	}
	return sources
}

func clipSource(c domain.Clip) twitch.Source {
	title := strings.TrimSpace(c.Title)
	if title == "" {
		title = "Twitch clip " + c.ID
	}
	if r := []rune(title); len(r) > 100 {
		title = string(r[:100])
	}
	return twitch.Source{Platform: "twitch", ClipID: c.ID, BroadcasterID: c.BroadcasterID, BroadcasterLogin: c.BroadcasterName, Title: title, URL: "https://clips.twitch.tv/" + c.ID, CreatedAt: c.CreatedAt, ObservedAt: c.ObservedAt, ViewCount: c.ViewCount, DurationSec: c.Duration, VideoID: c.VideoID, VODOffset: c.VODOffset, GameID: c.GameID, Language: c.Language, LanguageSource: c.LanguageSource, ThumbnailURL: c.ThumbnailURL, Growth: c.Growth}
}
func (s *Service) checkFlows(parent context.Context, only string, force bool) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()
	s.mu.Lock()
	s.checkCancel = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.checkCancel = nil; s.mu.Unlock() }()
	ctx, span := telemetry.Start(ctx, "collector.check_flows")
	defer span.End()
	defer func() { s.mu.Lock(); s.checking = false; s.mu.Unlock() }()
	started := time.Now().UTC()
	message := ""
	fetched, reserved, checked := 0, 0, 0
	defer func() {
		s.log.InfoContext(ctx, "collector check finished", "flows", checked, "fetched", fetched, "reserved", reserved, "duration_ms", time.Since(started).Milliseconds(), "reason", message)
		save, c := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer c()
		if err := s.store.SetLastCheck(save, started, message); err != nil {
			s.log.WarnContext(save, "collector check state unavailable", errorFields(err)...)
		}
	}()
	api := s.terex.(domain.FlowTerex)
	store, ok := s.store.(domain.FlowStore)
	if !ok {
		message = "Collector database does not support flow migration"
		return
	}
	helix, ok := s.twitch.(domain.FlowTwitch)
	if !ok {
		message = "Twitch flow discovery is unavailable"
		return
	}
	cfg, err := s.collectorConfig(ctx, api)
	if message = flowBlockReason(cfg, err, s.twitch.Ready()); message != "" {
		s.logGate(ctx, "discovery", message, err)
		return
	}
	channels, err := s.store.ListChannels(ctx)
	if err != nil {
		message = safeErrorMessage(err, "Не удалось загрузить каналы")
		return
	}
	catalogue := map[string]domain.Channel{}
	for _, ch := range channels {
		catalogue[ch.BroadcasterID] = ch
	}
	known := map[string]bool{}
	if indexed, ok := store.(interface {
		KnownClipIDs(context.Context) (map[string]bool, error)
	}); ok {
		known, err = indexed.KnownClipIDs(ctx)
		if err != nil {
			message = safeErrorMessage(err, "Не удалось получить уже обработанные источники")
			return
		}
	}
	lists := [][]domain.Clip{}
	flows := append([]twitch.Flow(nil), cfg.Flows...)
	sort.Slice(flows, func(i, j int) bool { return flows[i].ID < flows[j].ID })
	if rr, ok := store.(interface {
		RoundRobinStart(context.Context, int) (int, error)
	}); ok && len(flows) > 0 {
		start, e := rr.RoundRobinStart(ctx, len(flows))
		if e != nil {
			message = safeErrorMessage(e, "Не удалось обновить очередь потоков")
			return
		}
		flows = append(flows[start:], flows[:start]...)
	}
	windowResults, windowed := s.discoverWindowed(ctx, cfg, flows, catalogue, only, force, started)
	for _, flow := range flows {
		if !flow.Enabled || (only != "" && only != flow.ID) {
			continue
		}
		s.mu.Lock()
		last := s.flowStarted[flow.ID]
		s.mu.Unlock()
		interval := flow.PollIntervalMinutes
		if interval < 1 {
			interval = cfg.Settings.PollIntervalMinutes
		}
		if !force && time.Since(last) < time.Duration(interval)*time.Minute {
			continue
		}
		checked++
		s.mu.Lock()
		s.flowStarted[flow.ID] = started
		s.mu.Unlock()
		log := s.log.With("flow_id", flow.ID, "stage", "discovery")
		log.InfoContext(ctx, "collector flow check started", "source_mode", flow.SourceMode)
		// Rejected candidates must not exhaust the pagination stop budget.
		// A flow-local skip remains available to other flows.
		flowKnown := make(map[string]bool, len(known)+len(cfg.BlockedSourceIDs)+len(flow.ExcludedSourceIDs))
		for id := range known {
			flowKnown[id] = true
		}
		for _, id := range cfg.BlockedSourceIDs {
			flowKnown[id] = true
		}
		for _, id := range flow.ExcludedSourceIDs {
			flowKnown[id] = true
		}
		targets := []domain.Channel{{}}
		if flow.SourceMode != "category" {
			targets = nil
			if flow.ID == "default" && len(flow.BroadcasterIDs) == 0 {
				flow.BroadcasterIDs, err = store.LegacyBroadcasterIDs(ctx)
				if err != nil {
					message = safeErrorMessage(err, "Не удалось получить прежние источники основного потока")
					log.ErrorContext(ctx, message, errorFields(err)...)
					continue
				}
			}
			for _, id := range flow.BroadcasterIDs {
				ch, exists := catalogue[id]
				if !exists {
					if windowed {
						continue
					} // The bounded pass already attempted resolution.
					ch, err = helix.ResolveBroadcaster(ctx, id)
					if err == nil {
						ch, err = s.store.CreateChannel(ctx, ch)
					}
					if err != nil {
						message = safeErrorMessage(err, "Не удалось разрешить Twitch-канал")
						log.WarnContext(ctx, message, errorFields(err)...)
						continue
					}
					catalogue[id] = ch
				}
				if !ch.Paused && !ch.Archived {
					targets = append(targets, ch)
				}
			}
		}
		all := []domain.Clip{}
		for _, ch := range targets {
			var clips []domain.Clip
			var e error
			if windowed {
				if result := windowResults[discoveryKey(flow.ID, discoveryTarget(flow, ch))]; result != nil {
					clips = result.clips
					if len(clips) == 0 {
						e = result.err
					}
				}
			} else {
				// Discovery needs a bounded supply of candidates, not a daily
				// download cap. A disabled cap must not mean unlimited paging.
				clips, e = helix.GetFlowClips(ctx, flow, ch, started.Add(-time.Duration(flow.WindowHours)*time.Hour), started, admissionBufferSize*2, flowKnown)
			}
			if e != nil {
				message = safeErrorMessage(e, "Twitch недоступен")
				log.WarnContext(ctx, "collector source check failed", errorFields(e)...)
				if ch.ID != "" {
					_ = s.store.ChannelChecked(ctx, ch.ID, started, message)
				}
				continue
			}
			accepted := []domain.Clip{}
			for _, c := range clips {
				entry, exists := catalogue[c.BroadcasterID]
				if !exists {
					entry, e = s.store.CreateChannel(ctx, domain.Channel{BroadcasterID: c.BroadcasterID, Login: c.BroadcasterName, DisplayName: c.BroadcasterName})
					if e != nil {
						message = safeErrorMessage(e, "Не удалось сохранить канал")
						continue
					}
					catalogue[c.BroadcasterID] = entry
				}
				if entry.Paused || entry.Archived {
					continue
				}
				c.ChannelID = entry.ID
				c.FlowID = flow.ID
				c.FlowVersion = flow.Version
				accepted = append(accepted, c)
			}
			all = append(all, accepted...)
			if ch.ID != "" {
				_ = s.store.ChannelChecked(ctx, ch.ID, started, "")
			}
		}
		fetched += len(all)
		if err = s.store.UpsertClips(ctx, all); err != nil {
			message = safeErrorMessage(err, "Не удалось сохранить клипы")
			log.ErrorContext(ctx, message, errorFields(err)...)
			continue
		}
		observedFlow := flow
		observedFlow.ExcludedSourceIDs = append(append([]string(nil), flow.ExcludedSourceIDs...), cfg.BlockedSourceIDs...)
		if err = store.ObserveFlow(ctx, observedFlow, all); err != nil {
			message = safeErrorMessage(err, "Не удалось сохранить потребителей")
			continue
		}
		observationFailed := false
		if len(all) == 0 {
			if _, e := api.Observe(ctx, twitch.ObservationRequest{Epoch: cfg.Epoch, FlowID: flow.ID, FlowVersion: flow.Version, Sources: []twitch.Source{}}); e != nil {
				message = safeErrorMessage(e, "Не удалось записать проверку потока")
				observationFailed = true
			}
		}
		for offset := 0; offset < len(all); offset += observationBatchSize {
			end := offset + observationBatchSize
			if end > len(all) {
				end = len(all)
			}
			sources := observableSources(all[offset:end])
			imports, e := api.Observe(ctx, twitch.ObservationRequest{Epoch: cfg.Epoch, FlowID: flow.ID, FlowVersion: flow.Version, Sources: sources})
			if e != nil {
				message = safeErrorMessage(e, "Не удалось передать наблюдения в Terex")
				log.WarnContext(ctx, message, errorFields(e)...)
				observationFailed = true
				break
			}
			if e = store.SharedReady(ctx, imports); e != nil {
				message = safeErrorMessage(e, "Не удалось прикрепить готовые источники")
				observationFailed = true
				break
			}
			for _, imp := range imports {
				for n := range all {
					if all[n].ID == imp.Source.ClipID {
						all[n].Growth = imp.Source.Growth
					}
				}
			}
		}
		if observationFailed {
			continue
		}
		if windowed {
			if e := s.saveDiscoveryProgress(ctx, flow.ID, windowResults); e != nil {
				log.WarnContext(ctx, "collector discovery progress unavailable", "reason", safeErrorMessage(e, "progress unavailable"))
				continue
			}
		}
		if capacityAPI, ok := api.(interface {
			FlowCapacity(context.Context, string) (twitch.FlowCapacity, error)
		}); ok {
			capacity, e := capacityAPI.FlowCapacity(ctx, flow.ID)
			if e != nil {
				message = safeErrorMessage(e, "Не удалось проверить ближайшие места в календаре")
				log.WarnContext(ctx, "collector preparation capacity unavailable", "reason", message)
				continue
			}
			if capacity.Mode == "enforce" && !hasPreparationCapacity(capacity) {
				log.InfoContext(ctx, "collector flow calendar full", "reason", "Календарь ближайших выпусков заполнен; обновлены только метаданные", "horizon_end", capacity.HorizonEnd)
				continue
			}
		}
		matching := []domain.Clip{}
		excluded := map[string]bool{}
		for _, id := range flow.ExcludedSourceIDs {
			excluded[id] = true
		}
		for _, id := range cfg.BlockedSourceIDs {
			excluded[id] = true
		}
		for _, c := range all {
			maxAge := flow.MaxSourceAgeHours
			if maxAge <= 0 {
				maxAge = 72
			}
			if !excluded[c.ID] && (flow.Language == "" || flow.Language == c.Language && c.LanguageSource == "clip") && !c.CreatedAt.Add(time.Duration(maxAge)*time.Hour).Before(started) {
				matching = append(matching, c)
			}
		}
		selected := SelectRanked(matching, discoverySettings(cfg.Settings, flow), flow.RankingMode, started)
		lists = append(lists, selected)
		log.InfoContext(ctx, "collector flow check finished", "fetched", len(all), "eligible", len(selected))
	}
	// Reuse is observed before the backlog/download gate. Round-robin interleaves
	// candidate lists so one large category cannot spend the whole shared budget.
	if cfg.Settings.AdmissionMode == "enforce" {
		// The single reconciler owns admissions. Discovery only persists metadata.
		wake(s.wakeDownload)
		return
	}
	if err = s.downloadPressure(cfg); err != nil {
		message = safeErrorMessage(err, "Ожидание очереди Terex")
		return
	}
	// Shadow calculates the new distribution without changing current behavior.
	if api, ok := api.(interface {
		Admissions(context.Context, twitch.AdmissionRequest) (twitch.AdmissionResult, error)
	}); ok {
		_, _ = api.Admissions(ctx, twitch.AdmissionRequest{Epoch: cfg.Epoch, Limit: admissionBufferLimit(cfg.Settings)})
	}
	selected := []domain.Clip{}
	seen := map[string]bool{}
	for round := 0; ; round++ {
		found := false
		for _, list := range lists {
			if round < len(list) {
				found = true
				c := list[round]
				if !seen[c.ID] {
					seen[c.ID] = true
					selected = append(selected, c)
				}
			}
		}
		if !found {
			break
		}
	}
	reserved, err = s.store.Reserve(ctx, selected, cfg.Settings, started)
	if err != nil {
		message = "Не удалось зарезервировать скачивания: " + safeErrorMessage(err, "ошибка БД")
		s.log.ErrorContext(ctx, "collector reservation failed", errorFields(err)...)
	}
}

func (s *Service) flowWorkerOnce(ctx context.Context, api domain.FlowTerex) bool {
	cfg, err := s.collectorConfig(ctx, api)
	if reason := flowBlockReason(cfg, err, s.twitch.Ready()); reason != "" {
		s.logGate(ctx, "worker", reason, err)
		return false
	}
	ctx, release, err := s.beginResetWorker(ctx, cfg.Epoch)
	if err != nil {
		return false
	}
	defer release()
	store := s.store.(domain.FlowStore)
	allowNewPhysical := true
	if err = s.downloadPressure(cfg); err != nil {
		s.logGate(ctx, "worker", safeErrorMessage(err, "Новые скачивания ожидают очереди Terex"), err)
		var failure *domain.Failure
		if !errors.As(err, &failure) || failure.Code != "backlog_full" {
			return false
		}
		allowNewPhysical = false
	}
	if allowNewPhysical {
		s.logGate(ctx, "worker", "", nil)
	}
	queue, ok := s.store.(dispatchStore)
	if !ok {
		return false
	}
	var task *domain.Task
	if allowNewPhysical {
		task, err = queue.ClaimDownload(ctx, 30*time.Second)
	} else {
		task, err = queue.ClaimExistingDownload(ctx, 30*time.Second)
	}
	if err != nil {
		s.logGate(ctx, "task_claim", "Не удалось взять задание", err)
		return false
	}
	if task == nil {
		return false
	}
	if task.FlowID == "" {
		s.process(ctx, *task, cfg.Settings)
		return true
	}
	s.processFlow(ctx, *task, api, store)
	return true
}
func (s *Service) processFlow(parent context.Context, task domain.Task, api domain.FlowTerex, store domain.FlowStore) {
	if task.Uploaded != nil || task.Reservation.UploadGeneration > 0 && task.ErrorCode != "upload_missing" && task.TransferPhase != "reserved" {
		task.Completion = true
	}
	leaseTask := task
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	ctx, span := telemetry.Start(ctx, "collector.process_flow")
	defer span.End()
	log := s.log.With("clip_id", task.Clip.ID, "source_id", task.Clip.ID, "flow_id", task.FlowID, "attempt", task.Attempt)
	stage := "reservation"
	controlGuard := func(c context.Context) error {
		cfg, e := s.collectorConfig(c, api)
		if e != nil || cfg.Maintenance != nil || !cfg.Settings.Enabled || leaseTask.Epoch != cfg.Epoch {
			return domain.ErrPaused
		}
		for _, id := range cfg.BlockedSourceIDs {
			if id == leaseTask.Clip.ID {
				return &domain.Failure{Kind: "blocked", Code: "source_rejected", Message: "Источник исключён из всех потоков"}
			}
		}
		if !leaseTask.Completion {
			if !cfg.Settings.Enabled {
				return domain.ErrPaused
			}
			active, e := store.HasActiveConsumer(c, leaseTask.Clip.ID)
			if e != nil {
				return e
			}
			if !active {
				return domain.ErrPaused
			}
			ch, e := s.store.GetChannel(c, leaseTask.Clip.ChannelID)
			if e != nil {
				return e
			}
			if ch.Paused || ch.Archived {
				return domain.ErrPaused
			}
		}
		return nil
	}
	guard := func(c context.Context) error {
		if err := controlGuard(c); err != nil {
			return err
		}
		return s.store.ValidLease(c, leaseTask)
	}
	done := make(chan struct{})
	heartbeatStopped := make(chan struct{})
	defer func() { cancel(nil); close(done); <-heartbeatStopped }()
	go func() {
		defer close(heartbeatStopped)
		confirmed := time.Now().Add(30 * time.Second)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				request, end := context.WithTimeout(ctx, 3*time.Second)
				e := controlGuard(request)
				end()
				if e != nil {
					cancel(e)
					return
				}
				if e := s.renewFlowLease(ctx, leaseTask, confirmed); e != nil {
					cancel(e)
					return
				}
				confirmed = time.Now().Add(30 * time.Second)
			}
		}
	}()
	fail := func(e error) {
		save, c := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer c()
		if errors.Is(e, domain.ErrLeaseLost) || errors.Is(context.Cause(ctx), domain.ErrLeaseLost) {
			return
		}
		if errors.Is(e, domain.ErrPaused) || errors.Is(context.Cause(ctx), domain.ErrPaused) || parent.Err() != nil {
			_ = s.store.Defer(save, task, time.Now().Add(time.Minute))
			log.InfoContext(save, "collector task paused", "stage", stage)
			return
		}
		var f *domain.Failure
		if !errors.As(e, &f) {
			f = &domain.Failure{Kind: "internal", Message: safeErrorMessage(e, "Не удалось обработать клип"), Retryable: true}
		}
		if f.Kind == "preparation" {
			retry := time.Now().Add(time.Minute)
			if f.NextCheckAt != nil && f.NextCheckAt.After(time.Now()) {
				retry = *f.NextCheckAt
			}
			if deferred, ok := store.(interface {
				DeferForCapacity(context.Context, domain.Task, time.Time, string) error
			}); ok {
				if err := deferred.DeferForCapacity(save, task, retry, f.Message); err != nil {
					log.WarnContext(save, "collector capacity state unavailable", errorFields(err)...)
				}
			} else {
				_ = s.store.Defer(save, task, retry)
			}
			log.InfoContext(save, "collector waiting for preparation capacity", "stage", stage, "reason_code", f.Code, "reason", f.Message, "retry_at", retry.UTC())
			return
		}
		if f.Kind == "quota" {
			retry := time.Now().Add(time.Minute)
			if !strings.Contains(f.Message, "очередь") {
				cfg, err := s.collectorConfig(save, api)
				tz := "Asia/Tomsk"
				if err == nil {
					tz = cfg.Settings.Timezone
				}
				loc, err := time.LoadLocation(tz)
				if err != nil {
					loc = time.UTC
				}
				now := time.Now().In(loc)
				retry = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
			}
			if deferred, ok := store.(interface {
				DeferWithReason(context.Context, domain.Task, time.Time, string) error
			}); ok {
				if err := deferred.DeferWithReason(save, task, retry, f.Message); err != nil {
					log.WarnContext(save, "collector quota state unavailable", errorFields(err)...)
				}
			} else {
				_ = s.store.Defer(save, task, retry)
			}
			log.InfoContext(save, "collector task waiting for quota", "stage", stage, "reason", f.Message, "retry_at", retry.UTC())
			return
		}
		retry := RetryAt(task.Attempt, f, time.Now())
		if task.Completion {
			retry = CompletionRetryAt(task.Attempt, f, time.Now())
		}
		if f.Code == "flow_version_changed" {
			retry = nil
		}
		if retry != nil {
			log.WarnContext(save, "collector task retry scheduled", "stage", stage, "reason_code", f.Code, "reason", f.Message, "retry_at", retry)
		} else {
			log.ErrorContext(save, "collector task failed", "stage", stage, "reason_code", f.Code, "reason", f.Message)
		}
		if durable, ok := s.store.(interface {
			FailWithCode(context.Context, domain.Task, string, string, string, *time.Time) error
		}); ok {
			e = durable.FailWithCode(save, task, f.Kind, f.Message, f.Code, retry)
		} else {
			e = s.store.Fail(save, task, f.Kind, f.Message, retry)
		}
		if e != nil {
			log.WarnContext(save, "collector failure state unavailable", errorFields(e)...)
		} else if retry == nil {
			// The next worker tick releases the exact persisted failure before
			// requesting new admissions, including pre-upload generation zero.
			s.mu.Lock()
			s.lastFailureSyncAt = time.Time{}
			s.mu.Unlock()
		}
	}
	if err := guard(ctx); err != nil {
		fail(err)
		return
	}
	// A quarantined upload survives collector restarts. Retry verification for
	// this exact generation before considering another download or upload.
	if task.Completion || task.Uploaded != nil || task.Reservation.UploadGeneration > 0 && task.ErrorCode != "upload_missing" && task.TransferPhase != "reserved" {
		task.Completion = true
		stage = "verification"
		staged, ok := api.(stagedImportAPI)
		if !ok {
			fail(&domain.Failure{Kind: "import", Message: "Durable import verification unavailable", Retryable: true})
			return
		}
		if stageStore, ok := store.(interface {
			SetTaskStage(context.Context, domain.Task, string) error
		}); ok {
			if e := stageStore.SetTaskStage(ctx, task, "verifying"); e != nil {
				fail(e)
				return
			}
		}
		completed, e := staged.ImportStatus(ctx, task.Reservation.ImportID)
		if e != nil {
			fail(e)
			return
		}
		if completed.Epoch != task.Epoch || completed.UploadGeneration != task.Reservation.UploadGeneration || completed.ImportID != task.Reservation.ImportID {
			fail(&domain.Failure{Kind: "expired", Code: "upload_generation_changed", Message: "Загрузка уже заменена новой попыткой или сбросом"})
			return
		}
		if completed.Rejected || completed.MediaState == "expired" || completed.MediaState == "failed" || completed.MediaState == "" && completed.State == "failed" {
			fail(&domain.Failure{Kind: "blocked", Code: "upload_unavailable", Message: "Terex подтвердил отклонение, недоступность или ошибку этой загрузки; нужна ручная проверка состояния"})
			return
		}
		if !domain.MediaReady(completed) {
			if task.Uploaded == nil && completed.SHA256 != "" && completed.SizeBytes > 0 {
				media := domain.UploadedMedia{SHA256: completed.SHA256, SizeBytes: completed.SizeBytes, Phase: "uploaded"}
				if durable, ok := store.(interface {
					SaveUploaded(context.Context, domain.Task, domain.UploadedMedia) error
				}); ok {
					if e = durable.SaveUploaded(ctx, task, media); e != nil {
						fail(e)
						return
					}
					task.Uploaded = &media
				}
			}
			if task.Uploaded == nil || task.Uploaded.SHA256 == "" || task.Uploaded.SizeBytes <= 0 {
				fail(&domain.Failure{Kind: "reconciliation", Code: "upload_metadata_missing", Message: "Terex сохранил поколение загрузки, но контрольная сумма и размер не подтверждены. Нужна ручная сверка исходника; повторное скачивание не разрешено", Retryable: true})
				return
			}
			completed, e = staged.CompleteImport(ctx, task.Reservation, *task.Uploaded)
		}
		if e != nil {
			var remote *domain.Failure
			if errors.As(e, &remote) && remote.Code == "upload_missing" {
				if durable, ok := store.(interface {
					ClearUploaded(context.Context, domain.Task) error
				}); ok {
					if x := durable.ClearUploaded(ctx, task); x != nil {
						fail(x)
						return
					}
				}
				task.Uploaded = nil
				e = &domain.Failure{Kind: "upload", Code: "upload_missing", Message: remote.Message, Retryable: true}
			}
			fail(e)
			return
		}
		if e = guard(ctx); e != nil {
			fail(e)
			return
		}
		if completed.Epoch != task.Epoch || completed.UploadGeneration != task.Reservation.UploadGeneration || completed.ImportID != task.Reservation.ImportID {
			fail(&domain.Failure{Kind: "expired", Code: "upload_generation_changed", Message: "Ответ проверки относится к другой загрузке"})
			return
		}
		if !domain.MediaReady(completed) {
			// HTTP 202 is not media readiness. Keep the exact generation queued.
			fail(&domain.Failure{Kind: "verification", Code: "verification_pending", Message: "Terex принял проверку, но файл ещё не готов; ожидается завершение этой загрузки", Retryable: true})
			return
		}
		sha := completed.SHA256
		if task.Uploaded != nil {
			sha = task.Uploaded.SHA256
		}
		if e = s.store.Complete(ctx, task, completed.ImportID, sha); e != nil {
			fail(e)
			return
		}
		log.InfoContext(ctx, "collector import verification resumed", "import_id", completed.ImportID, "generation", task.Reservation.UploadGeneration)
		return
	}
	var err error
	task, err = store.PinActiveConsumer(ctx, task)
	if err != nil {
		fail(err)
		return
	}
	imp, err := s.reserveSharedImport(ctx, task, api, store)
	if err != nil {
		fail(err)
		return
	}
	task.Reservation = imp
	if err = store.SaveReservation(ctx, task, imp); err != nil {
		fail(err)
		return
	}
	if domain.MediaReady(imp) {
		if err = s.store.Complete(ctx, task, imp.ImportID, imp.SHA256); err != nil {
			fail(err)
		}
		log.InfoContext(ctx, "collector shared source reused", "import_id", imp.ImportID)
		return
	}
	stage = "download"
	log.InfoContext(ctx, "collector download started", "import_id", imp.ImportID, "generation", imp.UploadGeneration)
	file, err := s.downloader.Download(ctx, task.Clip.ID, task.Clip.Duration)
	if err != nil {
		fail(err)
		return
	}
	defer file.Cleanup()
	if file.ProbeDeferred {
		log.WarnContext(ctx, "collector local probe deferred", "import_id", imp.ImportID, "reason_code", "probe_unavailable", "reason", "Временная проверка перенесена на GPU; файл остаётся в карантине до полной проверки")
	}
	stage = "import"
	log.InfoContext(ctx, "collector upload started", "import_id", imp.ImportID, "size_bytes", file.Size)
	stageStore, hasStage := store.(interface {
		SetTaskStage(context.Context, domain.Task, string) error
	})
	if hasStage {
		if err = stageStore.SetTaskStage(ctx, task, "importing"); err != nil {
			fail(err)
			return
		}
	}
	if staged, ok := api.(stagedImportAPI); ok {
		durable, ok := store.(interface {
			SaveUploaded(context.Context, domain.Task, domain.UploadedMedia) error
		})
		if !ok {
			fail(errors.New("durable upload state unavailable"))
			return
		}
		media := domain.UploadedMedia{SHA256: file.SHA256, SizeBytes: file.Size, Phase: "uploading"}
		if err = durable.SaveUploaded(ctx, task, media); err != nil {
			fail(err)
			return
		}
		task.Uploaded = &media
		putErr := staged.PutImport(ctx, imp, *file, guard)
		if putErr == nil {
			media.Phase = "uploaded"
		} else {
			log.WarnContext(ctx, "collector upload result uncertain; verification will reconcile", "import_id", imp.ImportID, "reason", safeErrorMessage(putErr, "PUT response unavailable"))
		}
		queue, ok := s.store.(dispatchStore)
		if !ok {
			fail(errors.New("durable completion queue unavailable"))
			return
		}
		if err = queue.QueueCompletion(ctx, task, media); err != nil {
			fail(err)
			return
		}
		wake(s.wakeCompletion)
		log.InfoContext(ctx, "collector upload queued for verification", "import_id", imp.ImportID, "generation", imp.UploadGeneration, "phase", media.Phase, "dispatch_state", "verification_pending")
		return
	}
	uploadGuards := 0
	if err = api.UploadImport(ctx, imp, *file, func(c context.Context) error {
		uploadGuards++
		if uploadGuards == 2 && hasStage {
			stage = "verification"
			if e := stageStore.SetTaskStage(c, task, "verifying"); e != nil {
				return e
			}
		}
		return guard(c)
	}); err != nil {
		fail(err)
		return
	}
	if err = s.store.Complete(ctx, task, imp.ImportID, file.SHA256); err != nil {
		fail(err)
		return
	}
	log.InfoContext(ctx, "collector import completed", "import_id", imp.ImportID, "stage", "complete")
}

func hasPreparationCapacity(capacity twitch.FlowCapacity) bool {
	for _, f := range capacity.Formats {
		if f.Reason == "storage_low" || f.Reason == "storage_status_unknown" {
			continue
		}
		if f.FreeSlots > 0 || f.Format == "compilation" && f.PreparingSlots > 0 {
			return true
		}
	}
	return false
}

// Try another unpinned consumer before postponing a shared physical download.
// A coordinator grant, like an issued upload generation, keeps its owner.
func (s *Service) reserveSharedImport(ctx context.Context, task domain.Task, api domain.FlowTerex, store domain.FlowStore) (twitch.Import, error) {
	imp, err := api.ReserveImport(ctx, task)
	if err == nil || task.Reservation.ImportID != "" {
		return imp, err
	}
	var first *domain.Failure
	if !errors.As(err, &first) || first.Kind != "preparation" && first.Kind != "expired" {
		return imp, err
	}
	consumers, ok := store.(interface {
		ReservationConsumers(context.Context, string) ([]twitch.Flow, error)
	})
	if !ok {
		return imp, err
	}
	flows, loadErr := consumers.ReservationConsumers(ctx, task.Clip.ID)
	if loadErr != nil {
		return imp, loadErr
	}
	waiting := err
	for _, f := range flows {
		if f.ID == task.FlowID {
			continue
		}
		next := task
		next.FlowID, next.FlowVersion = f.ID, f.Version
		other, e := api.ReserveImport(ctx, next)
		if e == nil {
			// SaveReservation pins the returned owner as well as the generation.
			return other, nil
		}
		var failure *domain.Failure
		if !errors.As(e, &failure) || failure.Kind != "preparation" && failure.Kind != "expired" {
			return other, e
		}
		var previous *domain.Failure
		if failure.Kind == "preparation" && (!errors.As(waiting, &previous) || previous.Kind == "expired" ||
			failure.NextCheckAt != nil && previous.NextCheckAt != nil && failure.NextCheckAt.Before(*previous.NextCheckAt)) {
			waiting = e
		}
	}
	return imp, waiting
}
func (s *Service) Categories(ctx context.Context, q string) ([]twitch.Category, error) {
	api, ok := s.twitch.(domain.FlowTwitch)
	if !ok {
		return nil, errors.New("category search unavailable")
	}
	return api.Categories(ctx, q)
}

func (s *Service) ClipStatuses(ctx context.Context, flow string, ids []string) ([]domain.ClipStatus, error) {
	store, ok := s.store.(interface {
		ClipStatuses(context.Context, string, []string) ([]domain.ClipStatus, error)
	})
	if !ok {
		return nil, errors.New("flow clip status unavailable")
	}
	return store.ClipStatuses(ctx, flow, ids)
}

func (s *Service) RetryClip(ctx context.Context, id string) error {
	if _, ok := s.terex.(domain.FlowTerex); ok {
		return s.retryFlowClip(ctx, id)
	}
	store, ok := s.store.(interface {
		RetryClip(context.Context, string) error
	})
	if !ok {
		return errors.New("clip retry unavailable")
	}
	return store.RetryClip(ctx, id)
}

func (s *Service) FilteredClipStatuses(ctx context.Context, flow, state string) ([]domain.ClipStatus, error) {
	store, ok := s.store.(interface {
		FilteredClipStatuses(context.Context, string, string) ([]domain.ClipStatus, error)
	})
	if !ok {
		return nil, errors.New("flow status filter unavailable")
	}
	return store.FilteredClipStatuses(ctx, flow, state)
}
