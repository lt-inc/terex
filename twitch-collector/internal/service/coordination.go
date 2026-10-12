package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

type coordinationPressure struct {
	result     twitch.PressureResult
	observedAt time.Time
	err        error
}
type pressureAPI interface {
	Pressure(context.Context, twitch.PressureRequest) (twitch.PressureResult, error)
}
type dispatchStore interface {
	ClaimDownload(context.Context, time.Duration) (*domain.Task, error)
	ClaimExistingDownload(context.Context, time.Duration) (*domain.Task, error)
	ClaimCompletion(context.Context, time.Duration) (*domain.Task, error)
	QueueCompletion(context.Context, domain.Task, domain.UploadedMedia) error
	GrantRefs(context.Context, string, int) ([]twitch.PressureTaskRef, error)
	ApplyPressure(context.Context, twitch.PressureResult) error
	AuthorizeLegacy(context.Context) error
}

func wake(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Control polling is independent of the downloader and synchronous GPU verify.
// One mutation pass runs at a time and participates in reset draining.
func (s *Service) reconcile(ctx context.Context) {
	api := s.terex.(domain.FlowTerex)
	ticker := time.NewTicker(s.configInterval)
	defer ticker.Stop()
	for {
		// Leave room for transient DB/CPU contention on staging. This is only
		// the control read deadline; the 30s freshness gate and lease renewals
		// continue to fence work independently.
		request, cancel := context.WithTimeout(ctx, 8*time.Second)
		cfg, err := s.fetchCollectorConfig(request, api)
		cancel()
		if err != nil {
			s.logGate(ctx, "control", "Не удалось обновить настройки Terex", err)
		} else if skip, e := s.handleReset(ctx, cfg); e != nil {
			s.logGate(ctx, "reset", "Не удалось завершить остановку коллектора", e)
		} else if !skip {
			s.mu.Lock()
			start := !s.reconciling
			if start {
				s.reconciling = true
			}
			s.mu.Unlock()
			if start {
				go s.reconcilePass(ctx, api, cfg)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) reconcilePass(parent context.Context, api domain.FlowTerex, cfg twitch.CollectorConfig) {
	started := time.Now()
	defer func() { s.mu.Lock(); s.reconciling = false; s.mu.Unlock() }()
	ctx, release, err := s.beginResetWorker(parent, cfg.Epoch)
	if err != nil {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	store, ok := s.store.(domain.FlowStore)
	if !ok {
		return
	}
	configuration := make([]twitch.Flow, 0, len(cfg.Flows))
	for _, flow := range cfg.Flows {
		configuration = append(configuration, domain.ConfigurationFlow(flow))
	}
	raw, _ := json.Marshal(struct {
		Flows   []twitch.Flow
		Blocked []string
	}{configuration, cfg.BlockedSourceIDs})
	digest := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(digest[:])
	s.mu.Lock()
	changed := fingerprint != s.configFingerprint
	temporal := time.Since(s.lastTemporalAt) >= s.timeEligibility
	s.mu.Unlock()
	if changed {
		if err = syncFlowConfig(ctx, store, cfg); err != nil {
			s.logGate(ctx, "flow_sync", "Не удалось сохранить актуальные настройки потоков", err)
			return
		}
		s.mu.Lock()
		s.configFingerprint = fingerprint
		s.lastTemporalAt = time.Now()
		s.mu.Unlock()
	} else if temporal {
		if temporalStore, ok := s.store.(interface {
			RefreshEligibility(context.Context, []twitch.Flow) error
		}); ok {
			if err = temporalStore.RefreshEligibility(ctx, cfg.Flows); err != nil {
				s.logGate(ctx, "flow_sync", "Не удалось обновить актуальность источников", err)
				return
			}
		}
		s.mu.Lock()
		s.lastTemporalAt = time.Now()
		s.mu.Unlock()
	}
	s.mu.Lock()
	pressureDue := time.Since(s.lastPressureAt) >= s.timePressure
	s.mu.Unlock()
	if pressureDue {
		s.reconcilePressure(ctx, cfg)
	}
	// Do not launch another transaction when earlier reconciliation work has
	// exhausted this pass. Its next pass will still see admissions as due.
	remaining := time.Until(started.Add(30 * time.Second))
	if cfg.Settings.Enabled && cfg.Settings.AdmissionMode == "enforce" && remaining >= s.admissionsTimeout+time.Second {
		request, end := context.WithTimeout(ctx, s.admissionsTimeout)
		_, err = s.queueAdmissions(request, cfg)
		end()
		if err != nil {
			s.logGate(ctx, "admissions", "Не удалось получить назначения подготовки", err)
		}
	} else if cfg.Settings.AdmissionMode != "enforce" {
		if queue, ok := s.store.(dispatchStore); ok {
			if err = queue.AuthorizeLegacy(ctx); err != nil {
				s.logGate(ctx, "admissions", "Не удалось восстановить очередь скачивания", err)
			}
		}
	}
	s.reconcileRevalidations(ctx, api, cfg.Epoch)
	s.reconcileFailedImports(ctx, api, cfg.Epoch)
	if changed || temporal || pressureDue {
		s.log.InfoContext(ctx, "collector coordination reconciled", "epoch", cfg.Epoch, "flows", len(cfg.Flows), "config_changed", changed, "eligibility_refresh", temporal, "pressure_refresh", pressureDue, "duration_ms", time.Since(started).Milliseconds())
	}
	wake(s.wakeDownload)
	wake(s.wakeCompletion)
}

func (s *Service) reconcilePressure(ctx context.Context, cfg twitch.CollectorConfig) {
	api, ok := s.terex.(pressureAPI)
	store, ready := s.store.(dispatchStore)
	if !ok || !ready {
		return
	}
	s.mu.Lock()
	after := s.grantCursor
	s.mu.Unlock()
	for {
		refs, err := store.GrantRefs(ctx, after, 200)
		if err != nil {
			s.recordPressure(twitch.PressureResult{}, err)
			return
		}
		request, cancel := context.WithTimeout(ctx, 5*time.Second)
		result, err := api.Pressure(request, twitch.PressureRequest{Epoch: cfg.Epoch, Tasks: refs})
		cancel()
		if err != nil {
			s.recordPressure(result, err)
			return
		}
		if result.Epoch != cfg.Epoch || result.Maintenance {
			s.recordPressure(result, domain.ErrPaused)
			return
		}
		if err = store.ApplyPressure(ctx, result); err != nil {
			s.recordPressure(result, err)
			return
		}
		s.recordPressure(result, nil)
		valid, consumed, waiting := 0, 0, 0
		for _, task := range result.Tasks {
			if task.GrantState == "valid" {
				valid++
			} else if task.GrantState == "consumed" {
				consumed++
			} else {
				waiting++
			}
		}
		s.log.InfoContext(ctx, "collector assignments checked", "epoch", result.Epoch, "tasks", len(result.Tasks), "valid_assignments", valid, "consumed_assignments", consumed, "waiting_assignment", waiting, "pending_physical", result.PendingPhysical, "backlog_limit", result.BacklogLimit, "storage_state", result.Storage.State)
		if len(refs) < 200 {
			s.mu.Lock()
			s.grantCursor = ""
			s.mu.Unlock()
			return
		}
		after = refs[len(refs)-1].ClipID
		s.mu.Lock()
		s.grantCursor = after
		s.mu.Unlock()
	}
}
func (s *Service) recordPressure(result twitch.PressureResult, err error) {
	s.mu.Lock()
	s.pressure = coordinationPressure{result: result, observedAt: time.Now(), err: err}
	s.lastPressureAt = time.Now()
	s.mu.Unlock()
	if err != nil {
		s.logGate(context.Background(), "pressure", "Проверка разрешений и очереди недоступна; новые скачивания приостановлены", err)
	}
}

func (s *Service) downloadPressure(cfg twitch.CollectorConfig) error {
	s.mu.Lock()
	p := s.pressure
	s.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if p.observedAt.IsZero() || time.Since(p.observedAt) > 30*time.Second {
		return &domain.Failure{Kind: "pressure", Code: "pressure_stale", Message: "Нет свежего состояния очереди Terex", Retryable: true}
	}
	if p.result.Epoch != cfg.Epoch || p.result.Maintenance {
		return domain.ErrPaused
	}
	storage := p.result.Storage
	if storage.Required && (storage.State != "ok" || storage.ObservedAt.IsZero() || time.Since(storage.ObservedAt) > 3*time.Minute || storage.ObservedAt.After(time.Now().Add(time.Minute))) {
		code := "storage_status_unknown"
		if storage.State == "low" {
			code = "storage_low"
		}
		return &domain.Failure{Kind: "preparation", Code: code, Message: "Новые скачивания остановлены состоянием хранилища: " + p.result.Storage.Reason, Retryable: true}
	}
	if p.result.PendingPhysical >= p.result.BacklogLimit {
		return &domain.Failure{Kind: "preparation", Code: "backlog_full", Message: "Очередь новых источников заполнена; проверка и восстановление уже принятых загрузок продолжаются", Retryable: true}
	}
	return nil
}

func (s *Service) completionWorker(ctx context.Context) {
	api := s.terex.(domain.FlowTerex)
	for {
		processed := s.completionOnce(ctx, api)
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wakeCompletion:
		case <-time.After(5 * time.Second):
		}
	}
}
func (s *Service) completionOnce(ctx context.Context, api domain.FlowTerex) bool {
	cfg, err := s.collectorConfig(ctx, api)
	if err != nil || cfg.Maintenance != nil || !cfg.Settings.Enabled {
		return false
	}
	ctx, release, err := s.beginResetWorker(ctx, cfg.Epoch)
	if err != nil {
		return false
	}
	defer release()
	queue, ok := s.store.(dispatchStore)
	if !ok {
		return false
	}
	task, err := queue.ClaimCompletion(ctx, 30*time.Second)
	if err != nil {
		s.logGate(ctx, "completion_claim", "Не удалось взять загруженный файл для проверки", err)
		return false
	}
	if task == nil {
		return false
	}
	s.processFlow(ctx, *task, api, s.store.(domain.FlowStore))
	return true
}

// Renew transient transport failures only while the last confirmed lease still
// leaves room for another request. Lease loss/reset always stop immediately.
func (s *Service) renewFlowLease(ctx context.Context, task domain.Task, confirmed time.Time) error {
	var err error
	for i, delay := range []time.Duration{0, time.Second, 2 * time.Second} {
		if time.Until(confirmed) <= 5*time.Second {
			return domain.ErrLeaseLost
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		request, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = s.store.Renew(request, task, 30*time.Second)
		cancel()
		if err == nil {
			return nil
		}
		if errors.Is(err, domain.ErrLeaseLost) || errors.Is(err, domain.ErrPaused) {
			return err
		}
		if i == 2 {
			break
		}
	}
	return err
}
