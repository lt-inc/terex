package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	nethttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"terex/main-be/internal/config"
	"terex/main-be/internal/repository/collector"
	"terex/main-be/internal/repository/gpuclient"
	"terex/main-be/internal/repository/postgres"
	"terex/main-be/internal/repository/youtube"
	"terex/main-be/internal/service/assets"
	"terex/main-be/internal/service/jobs"
	"terex/main-be/internal/service/monitor"
	"terex/main-be/internal/service/publish"
	"terex/main-be/internal/service/storagelifecycle"
	tiktokservice "terex/main-be/internal/service/tiktok"
	twitchservice "terex/main-be/internal/service/twitch"
	"terex/main-be/internal/service/twitchmonitor"
	"terex/main-be/internal/service/twitchreset"
	workflowservice "terex/main-be/internal/service/workflow"
	"terex/main-be/internal/service/ytflow"
	tamqp "terex/main-be/internal/transport/amqp"
	"terex/main-be/internal/transport/cron"
	thttp "terex/main-be/internal/transport/http"
	"terex/proto/amqpx"
	"terex/proto/telemetry"
	"terex/proto/workflow"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	if cfg.DBStartupTimeout <= 0 {
		return errors.New("DB_STARTUP_TIMEOUT must be a positive duration")
	}
	if cfg.AdminUsername == "" || cfg.AdminPassword == "" || cfg.ServiceToken == "" {
		return errors.New("ADMIN_USERNAME, ADMIN_PASSWORD and TEREX_SERVICE_TOKEN are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTelemetry, err := telemetry.Setup(ctx, "terex-main-be")
	if err != nil {
		return err
	}
	defer func() {
		flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(flush)
	}()

	initCtx, endStartup := context.WithTimeout(ctx, cfg.DBStartupTimeout)
	defer endStartup()
	if err := postgres.MigrateContext(initCtx, cfg.DatabaseURL); err != nil {
		return err
	}
	store, err := postgres.New(initCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	endStartup()

	gpu, err := gpuclient.New(cfg.GpuGRPCAddr)
	if err != nil {
		return err
	}
	defer gpu.Close()

	br := amqpx.New(cfg.RabbitURL)
	if err := br.Connect(ctx); err != nil {
		return err
	}
	defer br.Close()

	jobSvc := &jobs.Service{Jobs: store, Assets: store, Accounts: store, Broker: br}
	assetSvc := &assets.Service{Assets: store, Accounts: store, Gpu: gpu, Importer: gpu.WorkflowGateway()}
	pubMgr := &publish.Manager{Clips: store, Jobs: store, Broker: br, Gpu: gpu}
	flow := &workflowservice.Service{Repo: store, Coordinator: store, Accounts: store, Clips: store, Gpu: gpu, Gateway: gpu.WorkflowGateway(), Broker: br, RedirectURL: cfg.OAuthRedirectURL, AppURL: cfg.AppPublicURL}
	pubMgr.Workflow = flow
	tiktokSvc := &tiktokservice.Service{Repo: store, Workflow: flow, Gpu: gpu, URL: cfg.TikTokUploaderURL, Token: cfg.TikTokUploaderToken, WorkerToken: cfg.TikTokWorkerToken, AppURL: cfg.AppPublicURL}
	mon := &monitor.Service{Channels: store, Feed: youtube.New(), Jobs: jobSvc}
	ytFlows := &ytflow.Service{Repo: store, Gateway: gpu.WorkflowGateway(), Feed: youtube.New(), Jobs: jobSvc,
		Cfg: ytflow.Config{QuotaDaily: cfg.YTQuotaDaily, ReservePublish: cfg.YTQuotaReservePublish, ReserveStats: cfg.YTQuotaReserveStats, RSSEvery: cfg.YTRSSInterval}}
	pubMgr.Titles = ytFlows

	twitchSvc := &twitchservice.Service{Repo: store, Gateway: gpu.IngestGateway(), Gpu: gpu}
	monitorSvc := &twitchmonitor.Service{Repo: store, Flows: store, Diagnostics: twitchSvc, Collector: collector.NewMonitoring(cfg.CollectorURL, cfg.CollectorToken), AppURL: cfg.AppPublicURL, FlowsPerTick: cfg.TwitchMonitorFlowsPerTick}
	storageSvc := storagelifecycle.New(store, gpu.StorageGateway(), cfg.TwitchCleanupEnabled)
	resetSvc := twitchreset.New(store, storageSvc, gpu.ResetGateway())
	storageSvc.ConfigurePressure(cfg.TwitchStoragePrometheusURL, cfg.TwitchStorageMinFreeBytes, cfg.TwitchStorageMinFreePercent)
	if err := storageSvc.PressureTick(ctx); err != nil {
		return fmt.Errorf("initialize Twitch storage admission: %w", err)
	}
	api := &thttp.API{Jobs: jobSvc, Assets: assetSvc, Publish: pubMgr, Monitor: mon, Workflow: flow,
		Twitch: twitchSvc, Storage: storageSvc, Reset: resetSvc, Auth: &thttp.OperatorAuth{Username: cfg.AdminUsername, Password: cfg.AdminPassword, ServiceToken: cfg.ServiceToken, AllowedOrigin: cfg.AppPublicURL},
		Collector: &thttp.CollectorProxy{URL: cfg.CollectorURL, Token: cfg.CollectorToken},
		TikTok:    tiktokSvc,
		YouTube:   ytFlows,
		News:      &thttp.NewsProxy{URL: cfg.NewsCollectorURL, Token: cfg.NewsCollectorToken}}
	srv := &nethttp.Server{Addr: cfg.HTTPAddr, Handler: thttp.Traced(thttp.AccessLog(api.Router())), ReadHeaderTimeout: 10 * time.Second}

	go func() { _ = tamqp.Run(ctx, br, jobSvc, pubMgr) }()
	go cron.Every(ctx, "monitor", cfg.MonitorInterval, mon)
	go cron.Every(ctx, "publish", cfg.PublishInterval, pubMgr)
	go cron.Every(ctx, "youtube flows", cfg.YTFlowsInterval, ytFlows)
	go cron.Every(ctx, "twitch compilation", time.Minute, twitchSvc)
	go cron.Every(ctx, "twitch monitoring", time.Minute, monitorSvc)
	go cron.Every(ctx, "twitch growth cleanup", time.Hour, cron.Func(func(ctx context.Context) error { return store.CleanupTwitchGrowth(ctx, time.Now().UTC()) }))
	go cron.Every(ctx, "twitch storage cleanup", cfg.TwitchCleanupInterval, storageSvc)
	go cron.Every(ctx, "twitch storage pressure", time.Minute, cron.Func(storageSvc.PressureTick))
	go cron.Every(ctx, "twitch reset", cfg.TwitchResetInterval, cron.Func(resetSvc.Tick))
	listener, err := net.Listen("tcp", cfg.WorkflowGRPCAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	coordinator := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	workflow.RegisterCoordinatorServer(coordinator, flow)
	defer coordinator.Stop()

	errc := make(chan error, 1)
	go func() {
		if e := coordinator.Serve(listener); e != nil {
			errc <- e
		}
	}()
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("main-be started", "http", cfg.HTTPAddr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
