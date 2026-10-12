// Package service runs the news pipeline: fetch feeds -> extract text -> deduplicate ->
// embed -> cluster stories per flow -> generate a post -> (approve) -> publish to Telegram.
// Every stage works from database state, so a restart resumes where it stopped.
package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/metrics"
	"terex/news-collector/internal/repository/extract"
	"terex/news-collector/internal/repository/feed"
	"terex/news-collector/internal/repository/images"
	"terex/news-collector/internal/repository/llm"
	"terex/news-collector/internal/repository/postgres"
	"terex/news-collector/internal/repository/telegram"
)

type Config struct {
	TickInterval       time.Duration
	ClusterThreshold   float64 // cosine at which an article joins an existing story
	CoveredThreshold   float64 // cosine at which a story counts as already published
	DupSimHashDistance int
	DupEmbedding       float64
	ArticleRetention   time.Duration
	MaxArticleAge      time.Duration // older feed items are ignored on ingest
	MaxExtractPerTick  int
	EmbedBatch         int
	ExtractWorkers     int
}

func DefaultConfig() Config {
	return Config{
		TickInterval:       time.Minute,
		ClusterThreshold:   0.72,
		CoveredThreshold:   0.85,
		DupSimHashDistance: 3,
		DupEmbedding:       0.97,
		ArticleRetention:   14 * 24 * time.Hour,
		MaxArticleAge:      72 * time.Hour,
		MaxExtractPerTick:  30,
		EmbedBatch:         32,
		ExtractWorkers:     4,
	}
}

type Service struct {
	cfg              Config
	store            *postgres.Store
	feeds            *feed.Client
	pages            *extract.Client
	llm              *llm.Client
	tg               *telegram.Client
	pexels           *images.Pexels
	checker          *images.Checker
	sealer           *Sealer
	log              *slog.Logger
	now              func() time.Time
	pipeMu           sync.Mutex // one pipeline pass at a time (tick, "fetch now", "generate now")
	pubMu            sync.Mutex
	stateMu          sync.Mutex
	lastTick         time.Time
	lastTickErr      string
	lastHousekeeping time.Time
}

func New(cfg Config, store *postgres.Store, feeds *feed.Client, pages *extract.Client, l *llm.Client, tg *telegram.Client, sealer *Sealer, log *slog.Logger) *Service {
	return &Service{cfg: cfg, store: store, feeds: feeds, pages: pages, llm: l, tg: tg, sealer: sealer, log: log, now: time.Now}
}

// WithImages enables stock photos (Pexels) and the image URL check. Both are optional.
func (s *Service) WithImages(p *images.Pexels, c *images.Checker) *Service {
	s.pexels, s.checker = p, c
	return s
}

// Run drives the pipeline until the context is cancelled.
func (s *Service) Run(ctx context.Context) {
	s.tick(ctx)
	t := time.NewTicker(s.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

// Healthy is false when the pipeline has not completed a pass for a long time.
func (s *Service) Healthy(started time.Time) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	last := s.lastTick
	if last.IsZero() {
		last = started
	}
	return s.now().Sub(last) < 15*s.cfg.TickInterval+10*time.Minute
}

func (s *Service) tick(ctx context.Context) {
	tctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	s.pipeMu.Lock()
	err := s.observe("pipeline", func() error { return s.pipeline(tctx) })
	s.pipeMu.Unlock()
	s.stateMu.Lock()
	s.lastTick = s.now()
	s.lastTickErr = ""
	if err != nil {
		s.lastTickErr = err.Error()
	}
	s.stateMu.Unlock()
	if err != nil {
		s.log.Warn("pipeline pass failed", "err", err)
	}
	// Publishing is independent from collection: a slow feed must not delay an approved post.
	if perr := s.observe("publish", func() error { return s.publishDue(tctx) }); perr != nil {
		s.log.Warn("publish pass failed", "err", perr)
	}
}

func (s *Service) pipeline(ctx context.Context) error {
	if n, err := s.store.RecoverSending(ctx, s.now().Add(-5*time.Minute)); err != nil {
		return err
	} else if n > 0 {
		s.log.Warn("posts interrupted while sending were marked failed", "count", n)
	}
	sources, err := s.store.DueSources(ctx)
	if err != nil {
		return err
	}
	for _, src := range sources {
		s.fetchSource(ctx, src)
	}
	if err = s.processNew(ctx); err != nil {
		return err
	}
	if err = s.embedPending(ctx); err != nil {
		s.log.Warn("embedding failed", "err", err)
	}
	flows, err := s.store.Flows(ctx)
	if err != nil {
		return err
	}
	for _, f := range flows {
		if !f.Enabled {
			continue
		}
		if err = s.assign(ctx, f); err != nil {
			s.log.Warn("clustering failed", "flow", f.ID, "err", err)
			_ = s.store.SetFlowResult(ctx, f.ID, false, "clustering: "+err.Error())
			continue
		}
		if _, err = s.generate(ctx, f, false); err != nil {
			s.log.Warn("generation failed", "flow", f.ID, "err", err)
			_ = s.store.SetFlowResult(ctx, f.ID, false, err.Error())
		}
	}
	s.housekeeping(ctx)
	return nil
}

func (s *Service) housekeeping(ctx context.Context) {
	if s.now().Sub(s.lastHousekeeping) < time.Hour {
		return
	}
	s.lastHousekeeping = s.now()
	if n, err := s.store.DeleteOldArticles(ctx, s.now().Add(-s.cfg.ArticleRetention)); err != nil {
		s.log.Warn("article cleanup failed", "err", err)
	} else if n > 0 {
		s.log.Info("old articles deleted", "count", n)
	}
	if n, err := s.store.ExpirePending(ctx, s.now().Add(-48*time.Hour)); err != nil {
		s.log.Warn("draft expiry failed", "err", err)
	} else if n > 0 {
		s.log.Info("stale drafts expired", "count", n)
	}
}

func (s *Service) Status(ctx context.Context) (domain.Status, error) {
	var st domain.Status
	if err := s.store.Counters(ctx, &st); err != nil {
		return st, err
	}
	st.LLMReady = s.llm.Ready()
	st.PexelsReady = s.pexels.Ready()
	st.SecretKeyReady = s.sealer != nil
	s.stateMu.Lock()
	if !s.lastTick.IsZero() {
		t := s.lastTick
		st.LastTickAt = &t
	}
	st.LastTickError = s.lastTickErr
	s.stateMu.Unlock()
	return st, nil
}

// observe runs one pipeline stage and records its duration and outcome.
func (s *Service) observe(stage string, run func() error) error {
	started := time.Now()
	err := run()
	metrics.PipelineDuration.WithLabelValues(stage).Observe(time.Since(started).Seconds())
	if err != nil {
		metrics.PipelineRuns.WithLabelValues(stage, "error").Inc()
		return err
	}
	metrics.PipelineRuns.WithLabelValues(stage, "ok").Inc()
	metrics.PipelineLastSuccess.WithLabelValues(stage).SetToCurrentTime()
	return nil
}

// MetricsCollector exports database-derived state gauges; register it once at startup.
func (s *Service) MetricsCollector() *metrics.Collector {
	return &metrics.Collector{
		Load:  s.store.MetricsSnapshot,
		Flags: func() (bool, bool) { return s.llm.Ready(), s.sealer != nil },
	}
}
