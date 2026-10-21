package ytdlp

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
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

// DownloadBackground fetches a single video capped at 1080p into dir and returns its path. A background
// loops silently under the clip, so audio and anything above the 1080x1920 canvas would only cost disk and
// render time. The extension is left to yt-dlp: a format that cannot be merged to mp4 keeps its own container.
func (c *Client) DownloadBackground(ctx context.Context, url, dir string) (path string, err error) {
	ctx, span := telemetry.Start(ctx, "yt-dlp background", attribute.String("youtube.url", url))
	defer func() { telemetry.End(span, err) }()
	start := time.Now()
	cmd := exec.CommandContext(ctx, "yt-dlp", "--no-playlist", "-f", "bv*[height<=1080][vcodec^=avc1]/bv*[height<=1080]/b[height<=1080]",
		"--merge-output-format", "mp4", url, "-o", filepath.Join(dir, "background.%(ext)s"))
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("yt-dlp: %w: %s", runErr, tail(out))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "background.*"))
	for _, f := range files {
		if !strings.HasSuffix(f, ".part") && !strings.HasSuffix(f, ".ytdl") {
			slog.InfoContext(ctx, "yt-dlp background finished", "url", url, "took", time.Since(start).Round(time.Millisecond))
			return f, nil
		}
	}
	return "", fmt.Errorf("yt-dlp produced no file")
}

func tail(b []byte) string {
	if len(b) > 2000 {
		b = b[len(b)-2000:]
	}
	return string(b)
}
