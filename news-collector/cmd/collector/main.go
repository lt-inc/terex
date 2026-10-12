package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata"

	"terex/news-collector/internal/repository/extract"
	"terex/news-collector/internal/repository/feed"
	"terex/news-collector/internal/repository/images"
	"terex/news-collector/internal/repository/llm"
	"terex/news-collector/internal/repository/postgres"
	"terex/news-collector/internal/repository/safehttp"
	"terex/news-collector/internal/repository/telegram"
	"terex/news-collector/internal/service"
	transport "terex/news-collector/internal/transport/http"
	"terex/proto/telemetry"

	"github.com/prometheus/client_golang/prometheus"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatal(log *slog.Logger, msg string, args ...any) {
	log.Error(msg, args...)
	os.Exit(1)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdown, err := telemetry.Setup(ctx, "terex-news-collector")
	if err != nil {
		slog.Error("telemetry initialization failed")
		os.Exit(1)
	}
	defer func() {
		flush, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = shutdown(flush)
	}()
	log := slog.Default().With("service", "news-collector")

	dsn, token := os.Getenv("DATABASE_URL"), os.Getenv("NEWS_SERVICE_TOKEN")
	if dsn == "" || token == "" {
		fatal(log, "DATABASE_URL and NEWS_SERVICE_TOKEN are required")
	}
	sealer, err := service.NewSealer(os.Getenv("NEWS_SECRET_KEY"))
	if err != nil {
		fatal(log, err.Error())
	}
	cfg := service.DefaultConfig()
	if raw := os.Getenv("NEWS_TICK_INTERVAL"); raw != "" {
		if cfg.TickInterval, err = time.ParseDuration(raw); err != nil || cfg.TickInterval < 10*time.Second {
			fatal(log, "NEWS_TICK_INTERVAL must be a duration of at least 10s")
		}
	}
	for name, dst := range map[string]*float64{"NEWS_CLUSTER_THRESHOLD": &cfg.ClusterThreshold, "NEWS_COVERED_THRESHOLD": &cfg.CoveredThreshold} {
		if raw := os.Getenv(name); raw != "" {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || v <= 0 || v >= 1 {
				fatal(log, name+" must be a number between 0 and 1")
			}
			*dst = v
		}
	}

	initCtx, end := context.WithTimeout(ctx, 60*time.Second)
	store, err := postgres.New(initCtx, dsn)
	if err != nil {
		fatal(log, "database unavailable")
	}
	defer store.Close()
	if err = store.Migrate(initCtx); err != nil {
		fatal(log, "migration failed", "err", err)
	}
	end()

	// Feed and article URLs are operator-supplied: the safe client refuses non-public addresses.
	web := safehttp.New(30*time.Second, false)
	svc := service.New(cfg, store,
		&feed.Client{HTTP: web},
		&extract.Client{HTTP: web},
		llm.New(os.Getenv("OPENROUTER_API_KEY"), env("OPENROUTER_MODEL", "deepseek/deepseek-v4.1-flash"), env("NEWS_EMBEDDING_MODEL", "openai/text-embedding-3-small")),
		telegram.New(os.Getenv("TELEGRAM_API_BASE")),
		sealer, log).
		WithImages(images.NewPexels(os.Getenv("PEXELS_API_KEY"), os.Getenv("PEXELS_API_BASE")), &images.Checker{HTTP: web})
	prometheus.MustRegister(svc.MetricsCollector())
	go svc.Run(ctx)

	started := time.Now()
	server := &http.Server{
		Addr:              env("HTTP_ADDR", ":8080"),
		Handler:           transport.Handler(&transport.API{Svc: svc, Token: token, Log: log, Started: started}),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	go func() {
		<-ctx.Done()
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(stop)
	}()
	log.Info("news collector started", "http", server.Addr, "llm_ready", os.Getenv("OPENROUTER_API_KEY") != "", "pexels_ready", os.Getenv("PEXELS_API_KEY") != "", "secret_key_ready", sealer != nil, "tick", cfg.TickInterval.String())
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(log, "http server failed")
	}
}
