package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr                    string
	DatabaseURL                 string
	DBStartupTimeout            time.Duration
	RabbitURL                   string
	GpuGRPCAddr                 string
	MonitorInterval             time.Duration
	PublishInterval             time.Duration
	WorkflowGRPCAddr            string
	OAuthRedirectURL            string
	AppPublicURL                string
	AdminUsername               string
	AdminPassword               string
	ServiceToken                string
	CollectorURL                string
	CollectorToken              string
	NewsCollectorURL            string
	NewsCollectorToken          string
	TikTokUploaderURL           string
	TikTokUploaderToken         string
	TikTokWorkerToken           string
	TwitchCleanupEnabled        bool
	TwitchCleanupInterval       time.Duration
	TwitchResetInterval         time.Duration
	TwitchMonitorFlowsPerTick   int
	TwitchStoragePrometheusURL  string
	TwitchStorageMinFreeBytes   int64
	TwitchStorageMinFreePercent float64
}

func Load() Config {
	return Config{
		HTTPAddr:                    env("HTTP_ADDR", ":8000"),
		DatabaseURL:                 os.Getenv("DATABASE_URL"),
		DBStartupTimeout:            startupTimeout(),
		RabbitURL:                   env("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/"),
		GpuGRPCAddr:                 env("GPU_GRPC_ADDR", "gpu-be:9090"),
		MonitorInterval:             duration("MONITOR_INTERVAL", 5*time.Minute),
		PublishInterval:             duration("PUBLISH_INTERVAL", 30*time.Second),
		WorkflowGRPCAddr:            env("WORKFLOW_GRPC_ADDR", ":9091"),
		OAuthRedirectURL:            env("GOOGLE_REDIRECT_URL", env("APP_PUBLIC_URL", "http://localhost:3000")+"/api/accounts/oauth/callback"),
		AppPublicURL:                env("APP_PUBLIC_URL", "http://localhost:3000"),
		AdminUsername:               os.Getenv("ADMIN_USERNAME"),
		AdminPassword:               os.Getenv("ADMIN_PASSWORD"),
		ServiceToken:                os.Getenv("TEREX_SERVICE_TOKEN"),
		CollectorURL:                os.Getenv("COLLECTOR_URL"),
		CollectorToken:              os.Getenv("COLLECTOR_SERVICE_TOKEN"),
		NewsCollectorURL:            os.Getenv("NEWS_COLLECTOR_URL"),
		NewsCollectorToken:          os.Getenv("NEWS_SERVICE_TOKEN"),
		TikTokUploaderURL:           os.Getenv("TIKTOK_UPLOADER_URL"),
		TikTokUploaderToken:         os.Getenv("TIKTOK_UPLOADER_SERVICE_TOKEN"),
		TikTokWorkerToken:           os.Getenv("TIKTOK_WORKER_TOKEN"),
		TwitchCleanupEnabled:        os.Getenv("TWITCH_CLEANUP_ENABLED") == "true",
		TwitchCleanupInterval:       positiveDuration("TWITCH_CLEANUP_INTERVAL", time.Hour),
		TwitchResetInterval:         positiveDuration("TWITCH_RESET_INTERVAL", 5*time.Second),
		TwitchMonitorFlowsPerTick:   int(positiveInt64("TWITCH_MONITOR_FLOWS_PER_TICK", 2)),
		TwitchStoragePrometheusURL:  os.Getenv("TWITCH_STORAGE_PROMETHEUS_URL"),
		TwitchStorageMinFreeBytes:   positiveInt64("TWITCH_STORAGE_MIN_FREE_BYTES", 1<<30),
		TwitchStorageMinFreePercent: percentage("TWITCH_STORAGE_MIN_FREE_PERCENT", 10),
	}
}

func startupTimeout() time.Duration {
	value := os.Getenv("DB_STARTUP_TIMEOUT")
	if value == "" {
		return 60 * time.Second
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0 // run rejects invalid startup configuration before connecting.
	}
	return d
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func duration(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return d
	}
	return def
}

func positiveDuration(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(k)); err == nil && d > 0 {
		return d
	}
	return def
}

func positiveInt64(k string, def int64) int64 {
	if n, err := strconv.ParseInt(os.Getenv(k), 10, 64); err == nil && n > 0 {
		return n
	}
	return def
}

func percentage(k string, def float64) float64 {
	if n, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil && n > 0 && n < 100 {
		return n
	}
	return def
}
