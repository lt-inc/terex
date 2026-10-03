package config

import (
	"os"
	"time"
)

type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	RabbitURL        string
	GpuGRPCAddr      string
	MonitorInterval  time.Duration
	PublishInterval  time.Duration
	WorkflowGRPCAddr string
	OAuthRedirectURL string
	AppPublicURL     string
}

func Load() Config {
	return Config{
		HTTPAddr:         env("HTTP_ADDR", ":8000"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		RabbitURL:        env("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/"),
		GpuGRPCAddr:      env("GPU_GRPC_ADDR", "gpu-be:9090"),
		MonitorInterval:  duration("MONITOR_INTERVAL", 5*time.Minute),
		PublishInterval:  duration("PUBLISH_INTERVAL", 30*time.Second),
		WorkflowGRPCAddr: env("WORKFLOW_GRPC_ADDR", ":9091"),
		OAuthRedirectURL: env("GOOGLE_REDIRECT_URL", env("APP_PUBLIC_URL", "http://localhost:3000")+"/api/accounts/oauth/callback"),
		AppPublicURL:     env("APP_PUBLIC_URL", "http://localhost:3000"),
	}
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
