package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	RabbitURL             string
	GRPCAddr              string
	S3Endpoint            string
	S3PublicEndpoint      string // endpoint embedded into presigned URLs (browser-reachable)
	S3AccessKey           string
	S3SecretKey           string
	S3UseSSL              bool
	StateDir              string
	TmpDir                string
	WhisperBin            string
	WhisperModel          string
	OpenRouterKey         string
	OpenRouterModel       string
	WorkersPerModule      int
	UseNVENC              bool
	ClipMinSec            float64  // highlights: hard lower bound for a clip (the LLM picks the length itself)
	ClipMaxSec            float64  // highlights: hard upper bound
	MaxClips              int      // highlights: max clips per video
	YamnetCmd             string   // YAMNet script command; empty disables audio-event hints
	PromptTags            []string // which hints the LLM sees
	LLMEffort             string   // OpenRouter reasoning effort; empty = default of the model
	LLMTimeout            time.Duration
	LLMChunkChars         int
	CoordinatorAddr       string
	PublisherWorkers      int
	RenderRevisionWorkers int
	GoogleClientID        string
	GoogleClientSecret    string
	GoogleRedirectURL     string
}

func Load() Config {
	return Config{
		RabbitURL:             env("RABBITMQ_URL", "amqp://guest:guest@rabbitmq:5672/"),
		GRPCAddr:              env("GRPC_ADDR", ":9090"),
		S3Endpoint:            env("S3_ENDPOINT", "minio:9000"),
		S3PublicEndpoint:      env("S3_PUBLIC_ENDPOINT", "localhost:9000"),
		S3AccessKey:           env("S3_ACCESS_KEY", "minioadmin"),
		S3SecretKey:           env("S3_SECRET_KEY", "minioadmin"),
		S3UseSSL:              env("S3_USE_SSL", "false") == "true",
		StateDir:              env("STATE_DIR", "/data/state"),
		TmpDir:                env("TMP_DIR", "/tmp/terex"),
		WhisperBin:            env("WHISPER_BIN", "whisper"),
		WhisperModel:          env("WHISPER_MODEL", "base"),
		OpenRouterKey:         os.Getenv("OPENROUTER_API_KEY"),
		OpenRouterModel:       env("OPENROUTER_MODEL", "openai/gpt-4o-mini"),
		WorkersPerModule:      envInt("WORKERS_PER_MODULE", 1),
		UseNVENC:              env("USE_NVENC", "false") == "true",
		ClipMinSec:            envFloat("CLIP_MIN_SEC", 15),
		ClipMaxSec:            envFloat("CLIP_MAX_SEC", 180),
		MaxClips:              envInt("MAX_CLIPS", 12),
		YamnetCmd:             env("YAMNET_CMD", "python3 /opt/terex/yamnet.py"),
		PromptTags:            splitCSV(env("PROMPT_TAGS", "energy:high,energy:spike,laugh,shout,gasp,music,fast,pause_before")),
		LLMEffort:             os.Getenv("LLM_REASONING_EFFORT"),
		LLMTimeout:            envDuration("LLM_TIMEOUT", 240*time.Second),
		LLMChunkChars:         envInt("LLM_CHUNK_CHARS", 80000),
		CoordinatorAddr:       env("MAIN_COORDINATOR_ADDR", "main-be:9091"),
		PublisherWorkers:      max(1, envInt("PUBLISHER_WORKERS", 2)),
		RenderRevisionWorkers: max(1, envInt("RENDER_REVISION_WORKERS", 1)),
		GoogleClientID:        os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:    os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:     os.Getenv("GOOGLE_REDIRECT_URL"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return d
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}

func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}
