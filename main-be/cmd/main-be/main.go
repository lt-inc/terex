package main

import (
	"context"
	"errors"
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
	"terex/main-be/internal/repository/gpuclient"
	"terex/main-be/internal/repository/postgres"
	"terex/main-be/internal/repository/youtube"
	"terex/main-be/internal/service/assets"
	"terex/main-be/internal/service/jobs"
	"terex/main-be/internal/service/monitor"
	"terex/main-be/internal/service/publish"
	twitchservice "terex/main-be/internal/service/twitch"
	workflowservice "terex/main-be/internal/service/workflow"
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

	if err := postgres.Migrate(cfg.DatabaseURL); err != nil {
		return err
	}
	store, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

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
	assetSvc := &assets.Service{Assets: store, Accounts: store, Gpu: gpu}
	pubMgr := &publish.Manager{Clips: store, Jobs: store, Broker: br, Gpu: gpu}
	flow := &workflowservice.Service{Repo: store, Coordinator: store, Accounts: store, Clips: store, Gpu: gpu, Gateway: gpu.WorkflowGateway(), Broker: br, RedirectURL: cfg.OAuthRedirectURL, AppURL: cfg.AppPublicURL}
	pubMgr.Workflow = flow
	mon := &monitor.Service{Channels: store, Feed: youtube.New(), Jobs: jobSvc}

	twitchSvc := &twitchservice.Service{Repo: store, Gateway: gpu.IngestGateway(), Gpu: gpu}
	api := &thttp.API{Jobs: jobSvc, Assets: assetSvc, Publish: pubMgr, Monitor: mon, Workflow: flow,
		Twitch: twitchSvc, Auth: &thttp.OperatorAuth{Username: cfg.AdminUsername, Password: cfg.AdminPassword, ServiceToken: cfg.ServiceToken, AllowedOrigin: cfg.AppPublicURL},
		Collector: &thttp.CollectorProxy{URL: cfg.CollectorURL, Token: cfg.CollectorToken}}
	srv := &nethttp.Server{Addr: cfg.HTTPAddr, Handler: thttp.Traced(thttp.AccessLog(api.Router())), ReadHeaderTimeout: 10 * time.Second}

	go func() { _ = tamqp.Run(ctx, br, jobSvc, pubMgr) }()
	go cron.Every(ctx, "monitor", cfg.MonitorInterval, mon)
	go cron.Every(ctx, "publish", cfg.PublishInterval, pubMgr)
	go cron.Every(ctx, "twitch compilation", time.Minute, twitchSvc)
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
