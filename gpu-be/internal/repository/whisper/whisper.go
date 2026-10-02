package whisper

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/proto/telemetry"
)

// Client runs the openai-whisper CLI (same engine as the old candidate service).
type Client struct {
	Bin   string
	Model string
}

func New(bin, model string) *Client { return &Client{Bin: bin, Model: model} }

func (c *Client) Transcribe(ctx context.Context, audioPath string) (segs []domain.Segment, err error) {
	ctx, span := telemetry.Start(ctx, "whisper transcribe", attribute.String("whisper.model", c.Model))
	defer func() { telemetry.End(span, err) }()
	start := time.Now()
	outDir, err := os.MkdirTemp("", "whisper-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(outDir)

	cmd := exec.CommandContext(ctx, c.Bin, audioPath,
		"--model", c.Model,
		"--word_timestamps", "True",
		"--output_format", "json",
		"--output_dir", outDir,
		"--verbose", "False",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("whisper: %w: %s", err, tail(out))
	}

	base := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	raw, err := os.ReadFile(filepath.Join(outDir, base+".json"))
	if err != nil {
		return nil, err
	}
	var res struct {
		Segments []domain.Segment `json:"segments"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("parse whisper json: %w", err)
	}
	span.SetAttributes(attribute.Int("whisper.segments", len(res.Segments)))
	slog.InfoContext(ctx, "whisper finished", "model", c.Model, "segments", len(res.Segments), "took", time.Since(start).Round(time.Millisecond))
	return res.Segments, nil
}

func tail(b []byte) string {
	if len(b) > 2000 {
		b = b[len(b)-2000:]
	}
	return string(b)
}
