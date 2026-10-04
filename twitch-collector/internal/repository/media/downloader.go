package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"terex/twitch-collector/internal/domain"
	"terex/twitch-collector/internal/repository/helix"
)

const MaxBytes int64 = 512 << 20

type Runner func(context.Context, string, []string, string) ([]byte, error)
type Downloader struct {
	YTDLP, FFprobe, TempDir string
	Run                     Runner
	Timeout                 time.Duration
	MaxSize                 int64
}

func New(ytdlp, probe, tmp string) *Downloader {
	return &Downloader{YTDLP: ytdlp, FFprobe: probe, TempDir: tmp, Run: run, Timeout: 120 * time.Second, MaxSize: MaxBytes}
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := b.limit - b.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.Buffer.Write(p)
	}
	return n, nil
}
func run(ctx context.Context, bin string, args []string, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	// Child processes receive no service tokens, client secret or database DSN.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "TMPDIR=" + dir, "TEMP=" + dir, "TMP=" + dir, "LANG=C.UTF-8", "SYSTEMROOT=" + os.Getenv("SYSTEMROOT")}
	output := &boundedBuffer{limit: 32 << 10}
	cmd.Stdout = output
	cmd.Stderr = output
	configureProcess(cmd)
	err := cmd.Run()
	return output.Bytes(), err
}
func (d *Downloader) Download(ctx context.Context, id string, expected float64) (*domain.Download, error) {
	if !helix.ClipIDPattern.MatchString(id) {
		return nil, &domain.Failure{Kind: "invalid", Message: "invalid Twitch clip ID"}
	}
	dir, err := os.MkdirTemp(d.TempDir, "twitch-clip-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	keep := false
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	limit := d.MaxSize
	if limit <= 0 {
		limit = MaxBytes
	}
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	downloadCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	finished := make(chan struct{})
	oversize := make(chan struct{}, 1)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-finished:
				return
			case <-downloadCtx.Done():
				return
			case <-ticker.C:
				var size int64
				_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, e error) error {
					if e == nil && !entry.IsDir() {
						if info, x := entry.Info(); x == nil {
							size += info.Size()
						}
					}
					return nil
				})
				if size > limit {
					select {
					case oversize <- struct{}{}:
					default:
					}
					cancel()
					return
				}
			}
		}
	}()
	path := filepath.Join(dir, "source.mp4")
	args := []string{"--ignore-config", "--no-playlist", "--no-progress", "--no-warnings", "--retries", "0", "--fragment-retries", "0", "--socket-timeout", "20", "--max-filesize", fmt.Sprint(limit), "--format", "best[ext=mp4]", "--output", path, "--", "https://clips.twitch.tv/" + id}
	runner := d.Run
	if runner == nil {
		runner = run
	}
	output, runErr := runner(downloadCtx, d.YTDLP, args, dir)
	close(finished)
	select {
	case <-oversize:
		return nil, &domain.Failure{Kind: "size", Message: "clip exceeds 512 MiB limit"}
	default:
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if runErr != nil {
		if errors.Is(downloadCtx.Err(), context.DeadlineExceeded) {
			return nil, &domain.Failure{Kind: "network", Message: "clip download timed out", Retryable: true}
		}
		return nil, classify(output)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, &domain.Failure{Kind: "invalid", Message: "downloader produced no video"}
	}
	if info.Size() > limit {
		return nil, &domain.Failure{Kind: "size", Message: "clip exceeds 512 MiB limit"}
	}
	probeCtx, probeCancel := context.WithTimeout(ctx, 15*time.Second)
	defer probeCancel()
	data, err := runner(probeCtx, d.FFprobe, []string{"-v", "error", "-show_streams", "-show_format", "-of", "json", path}, dir)
	if err != nil {
		return nil, &domain.Failure{Kind: "invalid", Message: "video inspection failed"}
	}
	result, err := inspect(data, expected)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	f.Close()
	if err != nil {
		return nil, err
	}
	result.Path = path
	result.Size = info.Size()
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	result.Cleanup = cleanup
	keep = true
	return result, nil
}
func classify(output []byte) *domain.Failure {
	text := strings.ToLower(string(output))
	kind := "unavailable"
	retry := false
	delay := time.Duration(0)
	switch {
	case strings.Contains(text, "429") || strings.Contains(text, "too many requests"):
		kind = "rate_limit"
		retry = true
		delay = 30 * time.Minute
	case strings.Contains(text, "403") || strings.Contains(text, "404") || strings.Contains(text, "captcha") || strings.Contains(text, "login") || strings.Contains(text, "private") || strings.Contains(text, "not available"):
		kind = "unavailable"
	case strings.Contains(text, "timed out") || strings.Contains(text, "timeout") || strings.Contains(text, "connection reset") || strings.Contains(text, "connection refused") || strings.Contains(text, "temporary failure") || strings.Contains(text, "name resolution") || strings.Contains(text, "http error 500") || strings.Contains(text, "http error 502") || strings.Contains(text, "http error 503") || strings.Contains(text, "http error 504"):
		kind = "network"
		retry = true
	}
	// Raw stderr can contain signed URLs. Only a fixed diagnostic leaves this layer.
	return &domain.Failure{Kind: kind, Message: "clip download failed (" + kind + ")", Retryable: retry, RetryAfter: delay}
}
func inspect(data []byte, expected float64) (*domain.Download, error) {
	var probe struct {
		Streams []struct {
			Type          string `json:"codec_type"`
			Width, Height int
		}
		Format struct {
			Duration string `json:"duration"`
		}
	}
	invalid := func() (*domain.Download, error) {
		return nil, &domain.Failure{Kind: "invalid", Message: "download is not a valid 5–60 second video"}
	}
	if json.Unmarshal(data, &probe) != nil {
		return invalid()
	}
	var duration float64
	if _, err := fmt.Sscan(probe.Format.Duration, &duration); err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 5 || duration > 60 {
		return invalid()
	}
	result := &domain.Download{Duration: duration}
	for _, stream := range probe.Streams {
		if stream.Type == "video" && stream.Width > 0 && stream.Height > 0 {
			result.Width = stream.Width
			result.Height = stream.Height
		}
		if stream.Type == "audio" {
			result.HasAudio = true
		}
	}
	if result.Width == 0 || result.Height == 0 || (expected > 0 && math.Abs(expected-duration) > 2) {
		return invalid()
	}
	return result, nil
}
