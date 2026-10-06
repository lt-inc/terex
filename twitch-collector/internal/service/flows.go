package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"terex/proto/telemetry"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"time"
)

func flowBlockReason(c twitch.CollectorConfig, err error, ready bool) string {
	if err != nil {
		return "Не удалось получить настройки Terex: " + safeErrorMessage(err, "проверьте связь и служебный токен")
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
	cfg, err := api.CollectorConfig(ctx)
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
	g.WindowHours = f.WindowHours
	g.MinViews = f.MinViews
	g.MinAgeMinutes = f.MinAgeMinutes
	g.MinDurationSec = f.MinDurationSec
	g.MaxDurationSec = f.MaxDurationSec
	return g
}
func clipSource(c domain.Clip) twitch.Source {
	title := strings.TrimSpace(c.Title)
	if title == "" {
		title = "Twitch clip " + c.ID
	}
	if r := []rune(title); len(r) > 100 {
		title = string(r[:100])
	}
	return twitch.Source{Platform: "twitch", ClipID: c.ID, BroadcasterID: c.BroadcasterID, BroadcasterLogin: c.BroadcasterName, Title: title, URL: "https://clips.twitch.tv/" + c.ID, CreatedAt: c.CreatedAt, ObservedAt: time.Now().UTC(), ViewCount: c.ViewCount, DurationSec: c.Duration, VideoID: c.VideoID, VODOffset: c.VODOffset, GameID: c.GameID, Language: c.Language, ThumbnailURL: c.ThumbnailURL}
}
func (s *Service) checkFlows(parent context.Context, only string, force bool) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Minute)
	defer cancel()
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
	cfg, err := api.CollectorConfig(ctx)
	if message = flowBlockReason(cfg, err, s.twitch.Ready()); message != "" {
		s.logGate(ctx, "discovery", message, err)
		return
	}
	if err = syncFlowConfig(ctx, store, cfg); err != nil {
		message = safeErrorMessage(err, "Не удалось сохранить потоки")
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
			clips, e := helix.GetFlowClips(ctx, flow, ch, started.Add(-time.Duration(flow.WindowHours)*time.Hour), started, cfg.Settings.DailyDownloads*2, flowKnown)
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
		if err = store.ObserveFlow(ctx, flow, all); err != nil {
			message = safeErrorMessage(err, "Не удалось сохранить потребителей")
			continue
		}
		observationFailed := false
		if len(all) == 0 {
			if _, e := api.Observe(ctx, twitch.ObservationRequest{FlowID: flow.ID, FlowVersion: flow.Version, Sources: []twitch.Source{}}); e != nil {
				message = safeErrorMessage(e, "Не удалось записать проверку потока")
				observationFailed = true
			}
		}
		for offset := 0; offset < len(all); offset += 100 {
			end := offset + 100
			if end > len(all) {
				end = len(all)
			}
			sources := []twitch.Source{}
			for _, c := range all[offset:end] {
				sources = append(sources, clipSource(c))
			}
			imports, e := api.Observe(ctx, twitch.ObservationRequest{FlowID: flow.ID, FlowVersion: flow.Version, Sources: sources})
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
		}
		if observationFailed {
			continue
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
			if !excluded[c.ID] && (flow.Language == "" || flow.Language == c.Language) {
				matching = append(matching, c)
			}
		}
		selected := Select(matching, discoverySettings(cfg.Settings, flow), started)
		lists = append(lists, selected)
		log.InfoContext(ctx, "collector flow check finished", "fetched", len(all), "eligible", len(selected))
	}
	// Reuse is observed before the backlog/download gate. Round-robin interleaves
	// candidate lists so one large category cannot spend the whole shared budget.
	pending, err := s.terex.PendingCount(ctx)
	if err != nil {
		message = "Не удалось проверить очередь Terex"
		return
	}
	if pending >= cfg.Settings.BacklogLimit {
		message = fmt.Sprintf("Ожидают обработки %d источников; предел %d", pending, cfg.Settings.BacklogLimit)
		return
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

func (s *Service) flowWorkerOnce(ctx context.Context, api domain.FlowTerex) {
	cfg, err := api.CollectorConfig(ctx)
	if reason := flowBlockReason(cfg, err, s.twitch.Ready()); reason != "" {
		s.logGate(ctx, "worker", reason, err)
		return
	}
	store := s.store.(domain.FlowStore)
	if err = syncFlowConfig(ctx, store, cfg); err != nil {
		s.logGate(ctx, "worker", "Не удалось обновить состояние потоков", err)
		return
	}
	pending, err := s.terex.PendingCount(ctx)
	if err != nil || pending >= cfg.Settings.BacklogLimit {
		s.logGate(ctx, "worker", "Очередь Terex недоступна или заполнена", err)
		return
	}
	s.logGate(ctx, "worker", "", nil)
	task, err := s.store.Claim(ctx, 30*time.Second)
	if err != nil {
		s.logGate(ctx, "task_claim", "Не удалось взять задание", err)
		return
	}
	if task == nil {
		return
	}
	if task.FlowID == "" {
		s.process(ctx, *task, cfg.Settings)
		return
	}
	s.processFlow(ctx, *task, api, store)
}
func (s *Service) processFlow(parent context.Context, task domain.Task, api domain.FlowTerex, store domain.FlowStore) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	ctx, span := telemetry.Start(ctx, "collector.process_flow")
	defer span.End()
	log := s.log.With("clip_id", task.Clip.ID, "source_id", task.Clip.ID, "flow_id", task.FlowID, "attempt", task.Attempt)
	stage := "reservation"
	guard := func(c context.Context) error {
		cfg, e := api.CollectorConfig(c)
		if e != nil || !cfg.Settings.Enabled {
			return domain.ErrPaused
		}
		if e = syncFlowConfig(c, store, cfg); e != nil {
			return e
		}
		active, e := store.HasActiveConsumer(c, task.Clip.ID)
		if e != nil {
			return e
		}
		if !active {
			return domain.ErrPaused
		}
		return s.store.ValidLease(c, task)
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e := guard(ctx); e != nil {
					cancel(e)
					return
				}
				if e := s.store.Renew(ctx, task, 30*time.Second); e != nil {
					cancel(e)
					return
				}
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
		if f.Kind == "quota" {
			retry := time.Now().Add(time.Minute)
			if !strings.Contains(f.Message, "очередь") {
				cfg, err := api.CollectorConfig(save)
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
		log.ErrorContext(save, "collector task failed", "stage", stage, "reason", f.Message, "retry_at", retry)
		if e = s.store.Fail(save, task, f.Kind, f.Message, retry); e != nil {
			log.WarnContext(save, "collector failure state unavailable", errorFields(e)...)
		}
	}
	if err := guard(ctx); err != nil {
		fail(err)
		return
	}
	var err error
	task, err = store.PinActiveConsumer(ctx, task)
	if err != nil {
		fail(err)
		return
	}
	imp, err := api.ReserveImport(ctx, task)
	if err != nil {
		fail(err)
		return
	}
	if err = store.SaveReservation(ctx, task, imp); err != nil {
		fail(err)
		return
	}
	task.Reservation = imp
	if imp.MediaState == "ready" || imp.State == "ready" {
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
