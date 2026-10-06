package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"terex/proto/telemetry"
	"terex/twitch-collector/internal/repository/helix"
	"terex/twitch-collector/internal/repository/media"
	"terex/twitch-collector/internal/repository/postgres"
	"terex/twitch-collector/internal/repository/terex"
	"terex/twitch-collector/internal/service"
	transport "terex/twitch-collector/internal/transport/http"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdownTelemetry, err := telemetry.Setup(ctx, "terex-twitch-collector")
	if err != nil {
		slog.Error("collector telemetry initialization failed")
		os.Exit(1)
	}
	defer func() {
		flush, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = shutdownTelemetry(flush)
	}()
	logger := slog.Default().With("service", "twitch-collector")
	token := os.Getenv("COLLECTOR_SERVICE_TOKEN")
	backendToken := os.Getenv("TEREX_SERVICE_TOKEN")
	dsn := os.Getenv("DATABASE_URL")
	if token == "" || backendToken == "" || dsn == "" {
		logger.Error("DATABASE_URL and both service tokens are required")
		os.Exit(1)
	}
	startupTimeout := 30 * time.Second
	if raw := os.Getenv("DB_STARTUP_TIMEOUT"); raw != "" {
		startupTimeout, err = time.ParseDuration(raw)
		if err != nil || startupTimeout <= 0 {
			logger.Error("DB_STARTUP_TIMEOUT must be a positive duration")
			os.Exit(1)
		}
	}
	initCtx, end := context.WithTimeout(ctx, startupTimeout)
	defer end()
	store, err := postgres.New(initCtx, dsn)
	if err != nil {
		logger.Error("collector database unavailable")
		os.Exit(1)
	}
	defer store.Close()
	if err = store.Migrate(initCtx); err != nil {
		logger.Error("collector migration failed")
		os.Exit(1)
	}
	end()
	tempDir := env("TMP_DIR", os.TempDir()+"/twitch-collector")
	if err = os.MkdirAll(tempDir, 0700); err != nil {
		logger.Error("collector temporary directory unavailable")
		os.Exit(1)
	}
	svc := service.New(store, helix.New(os.Getenv("TWITCH_CLIENT_ID"), os.Getenv("TWITCH_CLIENT_SECRET")), terex.New(env("TEREX_URL", env("TEREX_BASE_URL", "http://main-be:8000")), backendToken), media.New(env("YTDLP_BIN", "yt-dlp"), env("FFPROBE_BIN", "ffprobe"), tempDir), logger)
	svc.Start(ctx)
	server := &http.Server{Addr: env("HTTP_ADDR", ":8080"), Handler: transport.Handler(svc, token), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		stop, end := context.WithTimeout(context.Background(), 10*time.Second)
		defer end()
		_ = server.Shutdown(stop)
	}()
	logger.Info("Twitch collector started", "http", server.Addr, "credentials_ready", os.Getenv("TWITCH_CLIENT_ID") != "" && os.Getenv("TWITCH_CLIENT_SECRET") != "", "scheduler_tick_seconds", 60, "worker_tick_seconds", 5)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("collector HTTP server failed")
		os.Exit(1)
	}
	logger.Info("Twitch collector stopped")
}
