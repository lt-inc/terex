package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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
	// Proxy is the proxy URL (http://user:password@host:port) for yt-dlp alone, instead of the HTTPS_PROXY of
	// the process, which the rest of gpu-be keeps using. A residential proxy gets past the bot check that
	// datacenter IPs run into. Empty = yt-dlp uses the environment. The URL holds credentials: never log it.
	Proxy string
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
	if c.Proxy != "" {
		args = append(args, "--proxy", c.Proxy)
	}
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

// previewSeconds is how much of the video DownloadPreview keeps: the overlays are there from the first
// second, and a stretch of a few minutes is enough to see them against changing pictures.
const previewSeconds = 240

// previewMaxBytes caps what DownloadPreview reads: at 480p that is well over previewSeconds of video, and
// a long video must not be fetched whole (through a metered proxy every byte costs).
const previewMaxBytes = 40 << 20

// DownloadPreview fetches the start of a video without audio, at most 480p, into dir and returns its path.
// It is for looking at the picture (stills for the watermark editor), not for rendering.
//
// yt-dlp's own downloader writes the stream to stdout and is cut off at previewMaxBytes. --download-sections
// is not used: it hands the stream URL to ffmpeg, and googlevideo URLs are bound to the IP of the proxy
// yt-dlp used, so ffmpeg is answered with 403.
func (c *Client) DownloadPreview(ctx context.Context, url, dir string) (path string, err error) {
	ctx, span := telemetry.Start(ctx, "yt-dlp preview", attribute.String("youtube.url", url))
	defer func() { telemetry.End(span, err) }()
	start := time.Now()
	common, err := c.common(dir)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "yt-dlp", append(common, "--no-playlist", "-q", "--no-warnings", "-f",
		"bv*[height<=480][vcodec^=avc1]/bv*[height<=480]/b[height<=480]", "-o", "-", url)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	raw := filepath.Join(dir, "preview.raw")
	out, err := os.Create(raw)
	if err != nil {
		return "", err
	}
	if err = cmd.Start(); err != nil {
		out.Close()
		return "", fmt.Errorf("yt-dlp: %w", err)
	}
	n, copyErr := io.Copy(out, io.LimitReader(stdout, previewMaxBytes))
	closeErr := out.Close()
	capped := n >= previewMaxBytes
	if capped {
		cancel() // enough: stop yt-dlp, the rest of the video is not needed
	}
	waitErr := cmd.Wait()
	switch {
	case copyErr != nil:
		return "", fmt.Errorf("yt-dlp: %w", copyErr)
	case closeErr != nil:
		return "", closeErr
	case !capped && waitErr != nil:
		return "", fmt.Errorf("yt-dlp: %w: %s", waitErr, tail(stderr.Bytes()))
	case n == 0:
		return "", fmt.Errorf("yt-dlp produced no data: %s", tail(stderr.Bytes()))
	}
	// A cut-off file still announces the length of the whole video. Copying the streams into a new container
	// leaves a file whose duration is what is really there, which the frame sampler relies on.
	path = filepath.Join(dir, "preview.mp4")
	trim := exec.CommandContext(context.WithoutCancel(ctx), "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", raw,
		"-t", fmt.Sprint(previewSeconds), "-c", "copy", path)
	if b, e := trim.CombinedOutput(); e != nil {
		return "", fmt.Errorf("ffmpeg trim preview: %w: %s", e, tail(b))
	}
	_ = os.Remove(raw)
	slog.InfoContext(ctx, "yt-dlp preview finished", "url", url, "bytes", n, "capped", capped, "took", time.Since(start).Round(time.Millisecond))
	return path, nil
}

func tail(b []byte) string {
	if len(b) > 2000 {
		b = b[len(b)-2000:]
	}
	return string(b)
}
