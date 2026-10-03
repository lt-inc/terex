package ffmpeg

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"terex/gpu-be/internal/domain"
	"testing"
	"time"
)

func TestAssemblyMediaSmoke(t *testing.T) {
	if os.Getenv("MEDIA_SMOKE") != "1" {
		t.Skip("set MEDIA_SMOKE=1 with ffmpeg and fonts installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	files := []string{filepath.Join(dir, "audio.mp4"), filepath.Join(dir, "silent.mp4"), filepath.Join(dir, "portrait.mp4")}
	gen := func(file, size, rate string, audio bool) {
		t.Helper()
		args := []string{"-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=" + size + ":rate=" + rate}
		if audio {
			args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100")
		}
		args = append(args, "-t", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", file)
		if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("generate: %v %s", err, out)
		}
	}
	gen(files[0], "320x240", "25", true)
	gen(files[1], "640x360", "60", false)
	gen(files[2], "180x320", "24", true)
	c := New(false)
	out := filepath.Join(dir, "assembly.mp4")
	segments := []domain.AssemblySegment{{File: files[0], StartSec: 0, EndSec: 1, NextChannel: "alpha"}, {File: files[1], StartSec: 0, EndSec: 1, NextChannel: "beta"}, {File: files[2], StartSec: 0, EndSec: 1, NextChannel: "gamma"}}
	if err := c.Assemble(ctx, segments, out, 30); err != nil {
		t.Fatal(err)
	}
	p, err := c.Probe(ctx, out)
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 1920 || p.Height != 1080 || !p.HasAudio || math.Abs(p.DurationSec-5) > 0.2 {
		t.Fatalf("incorrect assembled media %+v", p)
	}
	if err = c.VerifyIntegrity(ctx, out); err != nil {
		t.Fatal(err)
	}
	vertical := filepath.Join(dir, "vertical.mp4")
	if err = c.Vertical(ctx, files[2], vertical, domain.VerticalSpec{Width: 1080, Blur: 10}); err != nil {
		t.Fatal(err)
	}
	p, err = c.Probe(ctx, vertical)
	if err != nil || p.Width != 1080 || p.Height != 1920 || math.Abs(p.DurationSec-1) > 0.15 {
		t.Fatalf("portrait truncated or wrong frame %+v %v", p, err)
	}
}
