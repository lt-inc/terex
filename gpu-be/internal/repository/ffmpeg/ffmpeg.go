package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/proto/telemetry"
)

type Client struct {
	// NVENC switches libx264 for h264_nvenc where we encode video.
	NVENC                  bool
	Threads                int
	VerificationWait       time.Duration
	Resources              domain.MediaResources
	encoding, verification chan struct{}
}

func New(nvenc bool) *Client {
	return &Client{NVENC: nvenc, Threads: 2, VerificationWait: 10 * time.Second, encoding: make(chan struct{}, 1), verification: make(chan struct{}, 1)}
}

// AcquireEncoding shares the heavy-media slot with legacy audio decoding.
func (c *Client) AcquireEncoding(ctx context.Context) (func(), error) { return c.acquire(ctx, false) }

func (c *Client) acquire(ctx context.Context, verification bool) (func(), error) {
	permit := c.encoding
	if verification {
		permit = c.verification
	}
	if permit == nil {
		return func() {}, nil
	} // compatibility for injected media clients
	var timeout <-chan time.Time
	var timer *time.Timer
	if verification {
		wait := c.VerificationWait
		if wait <= 0 {
			wait = 10 * time.Second
		}
		timer = time.NewTimer(wait)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case permit <- struct{}{}:
		return func() { <-permit }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-timeout:
		return nil, &domain.MediaResourceError{Code: "resource_wait", Detail: "video verification slot is busy; retry this import"}
	}
}

func (c *Client) run(ctx context.Context, op string, args ...string) (err error) {
	ctx, span := telemetry.Start(ctx, "ffmpeg "+op, attribute.Bool("ffmpeg.nvenc", c.NVENC))
	defer func() { telemetry.End(span, err) }()
	start := time.Now()
	attrs := []any{"op", op}
	defer func() {
		attrs = append(attrs, "duration_ms", time.Since(start).Milliseconds(), "result_code", domain.MediaResultCode(err))
		attrs = append(attrs, domain.MediaLogAttrs(ctx)...)
		if err != nil {
			slog.WarnContext(ctx, "ffmpeg failed", append(attrs, "err", err)...)
		} else {
			slog.InfoContext(ctx, "ffmpeg finished", append(attrs, "took", time.Since(start).Round(time.Millisecond))...)
		}
	}()
	if c.Resources != nil {
		var release func()
		ctx, release, err = c.Resources.Begin(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	verification := op == "integrity"
	release, err := c.acquire(ctx, verification)
	if err != nil {
		return err
	}
	defer release()
	waitMS := time.Since(start).Milliseconds()
	threads := max(1, c.Threads)
	if verification {
		threads = 1
	}
	full := []string{"-y", "-hide_banner", "-loglevel", "error", "-filter_threads", strconv.Itoa(threads), "-filter_complex_threads", strconv.Itoa(threads)}
	// threads is a per-input/output codec option; a single leading value only
	// limits the first decoder. Insert it for every file and for the encoder.
	for i, a := range args {
		if a == "-i" {
			full = append(full, "-threads", strconv.Itoa(threads))
		}
		if i == len(args)-1 {
			full = append(full, "-threads", strconv.Itoa(threads))
		}
		full = append(full, a)
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", full...)
	out, runErr := cmd.CombinedOutput()
	attrs = append(attrs, "queue_wait_ms", waitMS, "threads", threads, "nvenc", c.NVENC)
	if cmd.ProcessState != nil {
		attrs = append(attrs, "cpu_user_seconds", cmd.ProcessState.UserTime().Seconds(), "cpu_system_seconds", cmd.ProcessState.SystemTime().Seconds())
		attrs = append(attrs, "max_rss_kib", processMaxRSS(cmd.ProcessState))
	}
	var inputBytes int64
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			if info, e := os.Stat(args[i+1]); e == nil {
				inputBytes += info.Size()
			}
		}
	}
	attrs = append(attrs, "input_bytes", inputBytes)
	if len(args) > 0 {
		if info, e := os.Stat(args[len(args)-1]); e == nil {
			attrs = append(attrs, "output_bytes", info.Size())
		}
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if runErr != nil {
		if strings.Contains(strings.ToLower(string(out)), "no space left on device") {
			return &domain.MediaResourceError{Code: "scratch_full", Detail: "FFmpeg output filesystem is full"}
		}
		return fmt.Errorf("ffmpeg %s: %w: %s", op, runErr, tail(out))
	}
	return nil
}

// vcodec returns encoder args for quality crf/preset.
func (c *Client) vcodec(crf int, preset string) []string {
	if c.NVENC {
		return []string{"-c:v", "h264_nvenc", "-cq", strconv.Itoa(crf), "-preset", "p4"}
	}
	return []string{"-c:v", "libx264", "-crf", strconv.Itoa(crf), "-preset", preset}
}

// hwaccel returns NVDEC decode args to place immediately before a real
// (non-lavfi, non-image) file input when NVENC is enabled. Decoded frames
// still land in system memory, so existing CPU-only filters (ass, drawtext,
// geq, boxblur) keep working unchanged downstream.
func (c *Client) hwaccel() []string {
	if c.NVENC {
		return []string{"-hwaccel", "cuda"}
	}
	return nil
}

func (c *Client) VideoProxy(ctx context.Context, in, out string, height int) error {
	args := append(c.hwaccel(), "-i", in, "-vf", fmt.Sprintf("scale=-2:%d", height))
	args = append(args, c.vcodec(28, "veryfast")...)
	args = append(args, "-c:a", "aac", "-b:a", "64k", "-movflags", "+faststart", out)
	return c.run(ctx, "video_proxy", args...)
}

func (c *Client) Duration(ctx context.Context, in string) (float64, error) {
	b, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "json", in).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	var v struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return 0, err
	}
	duration, err := strconv.ParseFloat(v.Format.Duration, 64)
	if err != nil {
		return 0, err
	}
	if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
		return 0, fmt.Errorf("source has no finite positive duration")
	}
	return duration, nil
}

func (c *Client) AudioProxy(ctx context.Context, in, out string) error {
	return c.run(ctx, "audio_proxy", "-i", in, "-ac", "1", "-c:a", "aac", "-b:a", "64k", "-vn", out)
}

const circleAlpha = `geq=lum='p(X,Y)':cb='cb(X,Y)':cr='cr(X,Y)':a='if(lte(hypot(X-W/2\,Y-H/2)\,W/2)\,255\,0)'`

func (c *Client) Vertical(ctx context.Context, in, out string, v domain.VerticalSpec) error {
	w := v.Width
	if w == 0 {
		w = 1080
	}
	blur := v.Blur
	if blur == 0 {
		blur = 40
	}
	var filter string
	var args []string
	switch {
	case v.BgPath != "" && v.Circle:
		filter = fmt.Sprintf("[0:v]scale=%d:1920:force_original_aspect_ratio=increase,crop=%d:1920[bg];"+
			"[1:v]scale=%d:%d,format=yuva420p,%s[fg];[bg][fg]overlay=(W-w)/2:(H-h)/2,format=yuv420p,setsar=1", w, w, w, w, circleAlpha)
	case v.BgPath != "":
		filter = fmt.Sprintf("[0:v]scale=%d:1920:force_original_aspect_ratio=increase,crop=%d:1920[bg];"+
			"[1:v]scale=%d:-2[fg];[bg][fg]overlay=(W-w)/2:0,setsar=1", w, w, w)
	case v.Circle:
		filter = fmt.Sprintf("[0:v]scale=%d:1920:force_original_aspect_ratio=decrease,boxblur=%d:1,scale=%d:1920[bg];"+
			"[0:v]scale=%d:%d,format=yuva420p,%s[fg];[bg][fg]overlay=(W-w)/2:(H-h)/2,format=yuv420p,setsar=1", w, blur, w, w, w, circleAlpha)
	default:
		filter = fmt.Sprintf("[0:v]split=2[f][b];[f]scale=%d:1920:force_original_aspect_ratio=decrease[fg];[b]scale=%d:1920:force_original_aspect_ratio=increase,crop=%d:1920,boxblur=%d:1[bg];"+
			"[bg][fg]overlay=(W-w)/2:(H-h)/2,setsar=1,format=yuv420p", w, w, w, blur)
	}
	if v.BgPath != "" {
		args = append([]string{"-stream_loop", "-1"}, c.hwaccel()...)
		args = append(args, "-i", v.BgPath)
		args = append(args, c.hwaccel()...)
		args = append(args, "-i", in, "-filter_complex", filter, "-map", "1:a?", "-shortest")
	} else {
		args = append(c.hwaccel(), "-i", in, "-filter_complex", filter)
	}
	args = append(args, c.vcodec(20, "veryfast")...)
	args = append(args, "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", out)
	return c.run(ctx, "vertical", args...)
}

func (c *Client) BurnSubtitles(ctx context.Context, in, ass, out string) error {
	// ass path must be escaped for the filter graph
	esc := strings.NewReplacer(`\`, `\\`, `:`, `\:`, `'`, `\'`).Replace(ass)
	args := append(c.hwaccel(), "-i", in, "-vf", "ass="+esc)
	args = append(args, c.vcodec(18, "medium")...)
	args = append(args, "-c:a", "copy", out)
	return c.run(ctx, "subtitles", args...)
}

func (c *Client) Banner(ctx context.Context, in, bannerPath, out string, rectMode bool) error {
	const p = 20
	const r = 20
	isVideo := map[string]bool{".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".webm": true}[strings.ToLower(filepath.Ext(bannerPath))]
	var filter string
	var args []string
	if isVideo {
		if rectMode {
			filter = "[1:v]chromakey=0xFF00FF:similarity=0.2:blend=0.05,format=yuva420p[logo];[0:v][logo]overlay=0:0,format=yuv420p"
		} else {
			filter = fmt.Sprintf("[1:v]scale=w=%d:h=-2,chromakey=0xFF00FF:similarity=0.2:blend=0.05,format=yuva420p[logo];[0:v][logo]overlay=(W-w)/2:%d,format=yuv420p", 1080-2*p, p)
		}
		args = append(c.hwaccel(), "-i", in, "-stream_loop", "-1")
		args = append(args, c.hwaccel()...)
		args = append(args, "-i", bannerPath)
	} else {
		geq := fmt.Sprintf(`r=r(X\,Y):g=g(X\,Y):b=b(X\,Y):a=if(lte(hypot(max(0\,max(%d-X\,X-(W-%d)))\,max(0\,max(%d-Y\,Y-(H-%d))))\,%d)\,alpha(X\,Y)\,0)`, r, r, r, r, r)
		pos := fmt.Sprintf("%d:%d", p, p)
		if rectMode {
			pos = fmt.Sprintf("(W-w)/2:(H-h-%d)", p)
		}
		filter = fmt.Sprintf("[1:v][0:v]scale2ref=w=iw-%d:h=ih/6[logo_raw][base];[logo_raw]format=rgba,geq=%s[logo];[base][logo]overlay=%s,format=yuv420p", 2*p, geq, pos)
		args = append(c.hwaccel(), "-i", in, "-loop", "1", "-i", bannerPath)
	}
	args = append(args, "-filter_complex", filter+"[video]", "-map", "[video]", "-map", "0:a?")
	args = append(args, c.vcodec(18, "veryfast")...)
	args = append(args, "-c:a", "copy", "-shortest", out)
	return c.run(ctx, "banner", args...)
}

func (c *Client) Cut(ctx context.Context, in, out string, start, end float64) error {
	args := append(c.hwaccel(), "-i", in, "-ss", strconv.FormatFloat(start, 'f', 3, 64), "-t", strconv.FormatFloat(end-start, 'f', 3, 64))
	args = append(args, c.vcodec(23, "veryfast")...)
	args = append(args, "-c:a", "aac", "-b:a", "128k", out)
	return c.run(ctx, "cut", args...)
}

func tail(b []byte) string {
	if len(b) > 2000 {
		b = b[len(b)-2000:]
	}
	return string(b)
}
