package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
)

func TestRegionPixels(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		region        domain.WatermarkRegion
		want          pixelRegion
	}{
		{"even aligned", 1920, 1080, domain.WatermarkRegion{X: 0.5, Y: 0.5, W: 0.25, H: 0.25}, pixelRegion{960, 540, 480, 270}},
		{"rounds outwards to even", 1000, 1000, domain.WatermarkRegion{X: 0.1013, Y: 0.2007, W: 0.1, H: 0.1}, pixelRegion{100, 200, 102, 102}},
		{"clamped to the frame", 640, 360, domain.WatermarkRegion{X: 0.9, Y: 0.9, W: 0.1, H: 0.1}, pixelRegion{576, 324, 64, 36}},
		{"never thinner than a chroma pixel", 640, 360, domain.WatermarkRegion{X: 0.9999, Y: 0, W: 0.0001, H: 0.5}, pixelRegion{638, 0, 2, 180}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := regionPixels(c.width, c.height, c.region); got != c.want {
				t.Fatalf("regionPixels = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestWatermarkBlurGraph(t *testing.T) {
	g := watermarkBlurGraph(1280, 720, []domain.WatermarkRegion{{X: 0.75, Y: 0.05, W: 0.2, H: 0.1}, {X: 0.02, Y: 0.9, W: 0.2, H: 0.08}})
	for _, want := range []string{
		"[0:v]split=2[a0][c0];[c0]crop=256:72:960:36,gblur=sigma=24[b0];[a0][b0]overlay=960:36[o0];",
		"[o0]split=2[a1][c1];[c1]crop=258:58:24:648,gblur=sigma=19[b1];[a1][b1]overlay=24:648[o1];",
		"[o1]format=yuv420p[vout]",
	} {
		if !strings.Contains(g, want) {
			t.Fatalf("graph %q lacks %q", g, want)
		}
	}
}

func TestSampleTimes(t *testing.T) {
	got := sampleTimes(100, 4)
	if len(got) != 4 || got[0] <= 5 || got[3] >= 95 {
		t.Fatalf("times %v must stay inside 5..95", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("times %v must increase", got)
		}
	}
}

func TestWatermarkMediaSmoke(t *testing.T) {
	if os.Getenv("MEDIA_SMOKE") != "1" {
		t.Skip("set MEDIA_SMOKE=1 with ffmpeg installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.mp4")
	if out, err := exec.CommandContext(ctx, "ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "4", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", source).CombinedOutput(); err != nil {
		t.Fatalf("source: %v %s", err, out)
	}
	c := New(false)
	blurred := filepath.Join(dir, "blurred.mp4")
	if err := c.BlurRegions(ctx, source, blurred, []domain.WatermarkRegion{{X: 0.7, Y: 0.05, W: 0.25, H: 0.15}, {X: 0.05, Y: 0.8, W: 0.3, H: 0.15}}); err != nil {
		t.Fatal(err)
	}
	p, err := c.Probe(ctx, blurred)
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != 640 || p.Height != 360 || !p.HasAudio {
		t.Fatalf("blurred video %+v changed size or lost audio", p)
	}
	gray, err := c.GrayFrames(ctx, source, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(gray) != 6 || gray[0].W != 360 || gray[0].H != 202 || len(gray[0].Pix) != 360*202 {
		t.Fatalf("gray frames: %d, first %dx%d", len(gray), gray[0].W, gray[0].H)
	}
	stills, err := c.StillFrames(ctx, source, dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(stills) != 3 || len(stills[0].JPEG) < 1000 || stills[0].JPEG[0] != 0xFF || stills[0].JPEG[1] != 0xD8 {
		t.Fatalf("stills: %d", len(stills))
	}
}
