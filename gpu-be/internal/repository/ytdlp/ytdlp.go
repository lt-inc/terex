package ytdlp

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"terex/proto/telemetry"
)

type Client struct{}

func New() *Client { return &Client{} }

// Download mirrors preproc: best h264 video + m4a audio into dest.
func (c *Client) Download(ctx context.Context, url, dest string) (err error) {
	ctx, span := telemetry.Start(ctx, "yt-dlp download", attribute.String("youtube.url", url))
	defer func() { telemetry.End(span, err) }()
	start := time.Now()
	cmd := exec.CommandContext(ctx, "yt-dlp", "-S", "vcodec:h264,res,acodec:m4a", "--merge-output-format", "mp4", url, "-o", dest)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return fmt.Errorf("yt-dlp: %w: %s", runErr, tail(out))
	}
	slog.InfoContext(ctx, "yt-dlp finished", "url", url, "took", time.Since(start).Round(time.Millisecond))
	return nil
}

func tail(b []byte) string {
	if len(b) > 2000 {
		b = b[len(b)-2000:]
	}
	return string(b)
}
