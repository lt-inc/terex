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

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"terex/proto/telemetry"
	"terex/proto/workflow"
	"terex/stats-collector/internal/service"
	"terex/stats-collector/internal/store"
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
	shutdown, err := telemetry.Setup(ctx, "terex-stats-collector")
	if err != nil {
		slog.Error("telemetry initialization failed")
		os.Exit(1)
	}
	defer func() {
		flush, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = shutdown(flush)
	}()
	log := slog.Default().With("service", "stats-collector")

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatal(log, "DATABASE_URL is required")
	}
	gpuAddr := env("GPU_GRPC_ADDR", "gpu-be:9090")
	interval, err := time.ParseDuration(env("STATS_INTERVAL", "1h"))
	if err != nil || interval < time.Minute {
		fatal(log, "STATS_INTERVAL must be a duration of at least 1m")
	}
	maxVideos, err := strconv.Atoi(env("MAX_VIDEOS_PER_CHANNEL", "200"))
	if err != nil || maxVideos < 1 {
		fatal(log, "MAX_VIDEOS_PER_CHANNEL must be a positive integer")
	}
	fullDays, err := strconv.Atoi(env("FULL_RESOLUTION_DAYS", "14"))
	if err != nil || fullDays < 1 {
		fatal(log, "FULL_RESOLUTION_DAYS must be a positive integer")
	}

	initCtx, end := context.WithTimeout(ctx, 60*time.Second)
	st, err := store.New(initCtx, dsn)
	if err != nil {
		fatal(log, "database unavailable")
	}
	defer st.Close()
	if err = st.Migrate(initCtx); err != nil {
		fatal(log, "migration failed", "err", err)
	}
	end()

	// gpu-be holds the account credentials and calls YouTube; the collector never sees a token
	conn, err := grpc.NewClient(gpuAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fatal(log, "invalid GPU_GRPC_ADDR", "err", err)
	}
	defer conn.Close()

	started := time.Now()
	svc := service.New(service.Config{Interval: interval, MaxVideos: maxVideos, FullResolutionD: fullDays}, st, workflow.NewGatewayClient(conn), log)
	go svc.Run(ctx)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		if !svc.Healthy(started) {
			http.Error(w, "stale", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	server := &http.Server{Addr: env("HTTP_ADDR", ":8080"), Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		stop, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(stop)
	}()
	log.Info("stats collector started", "http", server.Addr, "interval", interval.String(), "max_videos", maxVideos, "gpu", gpuAddr)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(log, "http server failed")
	}
}
