package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"terex/proto/telemetry"
)

type Client struct {
	// CookiesFile is a Netscape cookies.txt of a logged-in YouTube session. YouTube answers datacenter and
	// proxy IPs with "Sign in to confirm you're not a bot"; the cookies are the way past that. Empty or absent file = none.
	CookiesFile string
	// JSRuntime names a JavaScript runtime installed on the host (node, deno, ...) for yt-dlp's YouTube
	// extraction: without one some formats are missing. Empty = yt-dlp's default (deno only).
	JSRuntime string
}

func New() *Client { return &Client{} }

// common returns the options every download shares. The cookies are copied into dir first: yt-dlp writes the
// cookie jar back when it finishes, which fails on a read-only mount (a Kubernetes secret), and a copy keeps
// one run from changing what the next one reads.
func (c *Client) common(dir string) ([]string, error) {
	var args []string
	if c.JSRuntime != "" {
		args = append(args, "--js-runtimes", c.JSRuntime)
	}
	if c.CookiesFile != "" {
		raw, err := os.ReadFile(c.CookiesFile)
		if errors.Is(err, fs.ErrNotExist) {
			// the secret is optional in the manifest: until the cookies are in Infisical the file is absent,
			// and downloads must keep working as they did without cookies
			slog.Warn("yt-dlp cookies file is absent, running without cookies", "path", c.CookiesFile)
			return args, nil
		}
		if err != nil {
			return nil, fmt.Errorf("yt-dlp cookies: %w", err)
		}
		copied := filepath.Join(dir, "cookies.txt")
		if err = os.WriteFile(copied, raw, 0o600); err != nil {
			return nil, fmt.Errorf("yt-dlp cookies: %w", err)
		}
		args = append(args, "--cookies", copied)
	}
	return args, nil
}

// Download mirrors preproc: best h264 video + m4a audio into dest.
func (c *Client) Download(ctx context.Context, url, dest string) (err error) {
	ctx, span := telemetry.Start(ctx, "yt-dlp download", attribute.String("youtube.url", url))
	defer func() { telemetry.End(span, err) }()
	start := time.Now()
	common, err := c.common(filepath.Dir(dest))
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "yt-dlp", append(common, "-S", "vcodec:h264,res,acodec:m4a", "--merge-output-format", "mp4", url, "-o", dest)...)
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
	common, err := c.common(dir)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "yt-dlp", append(common, "--no-playlist", "-f", "bv*[height<=1080][vcodec^=avc1]/bv*[height<=1080]/b[height<=1080]",
		"--merge-output-format", "mp4", url, "-o", filepath.Join(dir, "background.%(ext)s"))...)
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

// previewSeconds is how much of the video DownloadPreview fetches: the overlays are there from the first
// second, and a stretch of a few minutes is enough to see them against changing pictures.
const previewSeconds = 240

// DownloadPreview fetches the first minutes of a video without audio, capped at 720p, into dir and returns
// its path. It is for looking at the picture (stills for the watermark editor), not for rendering.
func (c *Client) DownloadPreview(ctx context.Context, url, dir string) (path string, err error) {
	ctx, span := telemetry.Start(ctx, "yt-dlp preview", attribute.String("youtube.url", url))
	defer func() { telemetry.End(span, err) }()
	start := time.Now()
	common, err := c.common(dir)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "yt-dlp", append(common, "--no-playlist", "-f", "bv*[height<=720][vcodec^=avc1]/bv*[height<=720]/b[height<=720]",
		"--download-sections", fmt.Sprintf("*0-%d", previewSeconds), "--merge-output-format", "mp4", url, "-o", filepath.Join(dir, "preview.%(ext)s"))...)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("yt-dlp: %w: %s", runErr, tail(out))
	}
	files, _ := filepath.Glob(filepath.Join(dir, "preview.*"))
	for _, f := range files {
		if !strings.HasSuffix(f, ".part") && !strings.HasSuffix(f, ".ytdl") {
			slog.InfoContext(ctx, "yt-dlp preview finished", "url", url, "took", time.Since(start).Round(time.Millisecond))
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
