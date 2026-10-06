package config

import (
	"os"
	"time"
)

type Config struct {
	HTTPAddr         string
	DatabaseURL      string
	DBStartupTimeout time.Duration
	RabbitURL        string
	GpuGRPCAddr      string
	MonitorInterval  time.Duration
	PublishInterval  time.Duration
	WorkflowGRPCAddr string
	OAuthRedirectURL string
	AppPublicURL     string
	AdminUsername    string
	AdminPassword    string
	ServiceToken     string
	CollectorURL     string
	CollectorToken   string
}

func Load() Config {
	return Config{
		HTTPAddr:         env("HTTP_ADDR", ":8000"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		DBStartupTimeout: startupTimeout(),
		RabbitURL:        env("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/"),
		GpuGRPCAddr:      env("GPU_GRPC_ADDR", "gpu-be:9090"),
		MonitorInterval:  duration("MONITOR_INTERVAL", 5*time.Minute),
		PublishInterval:  duration("PUBLISH_INTERVAL", 30*time.Second),
		WorkflowGRPCAddr: env("WORKFLOW_GRPC_ADDR", ":9091"),
		OAuthRedirectURL: env("GOOGLE_REDIRECT_URL", env("APP_PUBLIC_URL", "http://localhost:3000")+"/api/accounts/oauth/callback"),
		AppPublicURL:     env("APP_PUBLIC_URL", "http://localhost:3000"),
		AdminUsername:    os.Getenv("ADMIN_USERNAME"),
		AdminPassword:    os.Getenv("ADMIN_PASSWORD"),
		ServiceToken:     os.Getenv("TEREX_SERVICE_TOKEN"),
		CollectorURL:     os.Getenv("COLLECTOR_URL"),
		CollectorToken:   os.Getenv("COLLECTOR_SERVICE_TOKEN"),
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
