package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	gpuv1 "terex/proto/gpu/v1"

	"terex/gpu-be/internal/config"
	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/audio"
	credentialrepo "terex/gpu-be/internal/repository/credentials"
	"terex/gpu-be/internal/repository/ffmpeg"
	"terex/gpu-be/internal/repository/jobstate"
	"terex/gpu-be/internal/repository/llm"
	"terex/gpu-be/internal/repository/minio"
	"terex/gpu-be/internal/repository/whisper"
	"terex/gpu-be/internal/repository/ytdlp"
	"terex/gpu-be/internal/service/candidate"
	"terex/gpu-be/internal/service/googleauth"
	"terex/gpu-be/internal/service/ingestgateway"
	"terex/gpu-be/internal/service/preproc"
	"terex/gpu-be/internal/service/publisher"
	"terex/gpu-be/internal/service/publishworker"
	"terex/gpu-be/internal/service/renderer"
	"terex/gpu-be/internal/service/renderworker"
	"terex/gpu-be/internal/service/reporter"
	"terex/gpu-be/internal/service/storagegw"
	"terex/gpu-be/internal/service/workflowgateway"
	tamqp "terex/gpu-be/internal/transport/amqp"
	tgrpc "terex/gpu-be/internal/transport/grpc"
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
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTelemetry, err := telemetry.Setup(ctx, "terex-gpu-be")
	if err != nil {
		return err
	}
	defer func() {
		flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(flush)
	}()

	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return err
	}
	state, err := jobstate.Open(cfg.StateDir)
	if err != nil {
		return err
	}
	defer state.Close()

	store, err := minio.New(cfg.S3Endpoint, cfg.S3PublicEndpoint, cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3UseSSL)
	if err != nil {
		return err
	}
	if err := store.EnsureBuckets(ctx, domain.BucketDownloads, domain.BucketProxy, domain.BucketTranscripts,
		domain.BucketClips, domain.BucketBanners, domain.BucketBackgrounds, domain.BucketCookies, domain.BucketCredentials, ingestgateway.Bucket); err != nil {
		return err
	}

	br := amqpx.New(cfg.RabbitURL)
	if err := br.Connect(ctx); err != nil {
		return err
	}
	defer br.Close()

	media := ffmpeg.New(cfg.UseNVENC)
	rep := &reporter.Reporter{State: state, Broker: br}
	gw := &storagegw.Service{Store: store}
	auth := googleauth.New(&credentialrepo.Repository{Store: store}, cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
	coordConn, err := grpc.NewClient(cfg.CoordinatorAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer coordConn.Close()
	coord := workflow.NewCoordinatorClient(coordConn)
	workerID, _ := os.Hostname()
	rendererSvc := &renderer.Service{Store: store, Media: media, Broker: br, State: state, Rep: rep, TmpDir: cfg.TmpDir}

	svcs := tamqp.Services{
		Preproc: &preproc.Service{Store: store, Dl: ytdlp.New(), Media: media, Broker: br, Rep: rep, TmpDir: cfg.TmpDir},
		Candidate: &candidate.Service{Store: store, STT: whisper.New(cfg.WhisperBin, cfg.WhisperModel), LLM: newLLM(cfg), Audio: audio.New(cfg.YamnetCmd),
			Limits: domain.ClipLimits{MinSec: cfg.ClipMinSec, MaxSec: cfg.ClipMaxSec, MaxClips: cfg.MaxClips}, PromptTags: cfg.PromptTags, Broker: br, Rep: rep, TmpDir: cfg.TmpDir},
		Renderer:         rendererSvc,
		Publisher:        &publisher.Service{Pub: publisher.Manual{}, Broker: br},
		Publication:      &publishworker.Worker{Coordinator: coord, Store: store, Auth: auth, TmpDir: cfg.TmpDir, WorkerID: workerID + "-publish"},
		RenderRevision:   &renderworker.Worker{Coordinator: coord, Renderer: rendererSvc, WorkerID: workerID + "-render"},
		PublisherWorkers: cfg.PublisherWorkers, RenderRevisionWorkers: cfg.RenderRevisionWorkers,
	}

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	gs := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()))
	gpuv1.RegisterGpuServiceServer(gs, &tgrpc.Server{State: state, GW: gw})
	workflow.RegisterGatewayServer(gs, &workflowgateway.Service{Auth: auth, Store: store, Media: media, TmpDir: cfg.TmpDir})
	workflow.RegisterIngestGatewayServer(gs, &ingestgateway.Service{Store: store, Media: media, TmpDir: cfg.TmpDir})
	go func() { _ = gs.Serve(lis) }()
	slog.Info("gpu-be started", "grpc", cfg.GRPCAddr)

	errc := make(chan error, 1)
	go func() { errc <- tamqp.Run(ctx, br, cfg.WorkersPerModule, svcs) }()
	<-ctx.Done()
	gs.GracefulStop()
	return <-errc
}

func newLLM(cfg config.Config) *llm.Client {
	c := llm.New(cfg.OpenRouterKey, cfg.OpenRouterModel)
	c.Effort, c.ChunkChars = cfg.LLMEffort, cfg.LLMChunkChars
	c.HTTP.Timeout = cfg.LLMTimeout
	return c
}
