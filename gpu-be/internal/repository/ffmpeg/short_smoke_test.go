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

// This opt-in test runs in the same Ubuntu/FFmpeg image as production. Keep it
// small: accurate trim, source audio, silent portrait, and both static assets.
func TestShortSinglePassMediaSmoke(t *testing.T) {
	if os.Getenv("MEDIA_SMOKE") != "1" {
		t.Skip("set MEDIA_SMOKE=1 in production media runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.CommandContext(ctx, "ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("generate: %v %s", err, out)
		}
	}
	source := filepath.Join(dir, "source.mp4")
	run("-f", "lavfi", "-i", "testsrc2=size=640x360:rate=60", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "2", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", source)
	silent := filepath.Join(dir, "silent.mp4")
	run("-f", "lavfi", "-i", "testsrc2=size=180x320:rate=24", "-t", "2", "-threads", "1", "-c:v", "libx264", "-pix_fmt", "yuv420p", silent)
	png, jpg := filepath.Join(dir, "banner.png"), filepath.Join(dir, "banner.jpg")
	for _, asset := range []string{png, jpg} {
		run("-f", "lavfi", "-i", "color=red:size=100x50", "-frames:v", "1", "-threads", "1", asset)
	}
	c := New(false)
	for i, tc := range []struct {
		source, banner string
		audio          bool
		rect           bool
	}{{source, "", true, true}, {source, png, true, true}, {source, jpg, true, false}, {silent, "", false, true}} {
		out := filepath.Join(dir, "out"+string(rune('a'+i))+".mp4")
		if err := c.RenderShort(ctx, domain.ShortRenderSpec{Source: tc.source, Banner: tc.banner, StartSec: 0.35, EndSec: 1.6, Rectangle: tc.rect}, out); err != nil {
			t.Fatal(err)
		}
		p, err := c.Probe(ctx, out)
		if err != nil || p.Width != 1080 || p.Height != 1920 || p.HasAudio != tc.audio || math.Abs(p.DurationSec-1.25) > 0.1 {
			t.Fatalf("trim/audio/profile %+v %v", p, err)
		}
		if err = c.VerifyIntegrity(ctx, out); err != nil {
			t.Fatal(err)
		}
	}
}
