package watermark_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/ffmpeg"
	"terex/gpu-be/internal/service/watermark"
)

// TestDetectRealVideo runs the whole chain on a real encode: ffmpeg seeks, scales and reads the stills,
// the detector finds a static logo over moving gradients, and the blur removes it again.
func TestDetectRealVideo(t *testing.T) {
	if os.Getenv("MEDIA_SMOKE") != "1" {
		t.Skip("set MEDIA_SMOKE=1 with ffmpeg installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	ff := func(args ...string) {
		t.Helper()
		if out, err := exec.CommandContext(ctx, "ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
	}
	plain := filepath.Join(dir, "plain.mp4")
	ff("-f", "lavfi", "-i", "gradients=size=640x360:rate=10:speed=0.3:nb_colors=6:seed=7", "-vf", "noise=alls=10:allf=t+u", "-t", "30", "-c:v", "libx264", "-pix_fmt", "yuv420p", plain)
	marked := filepath.Join(dir, "marked.mp4")
	// a 120x40 logo, 70% opaque, 24px from the top right corner
	ff("-i", plain, "-f", "lavfi", "-i", "smptebars=size=120x40:rate=10", "-filter_complex",
		"[1:v]format=rgba,colorchannelmixer=aa=0.7[logo];[0:v][logo]overlay=W-w-24:24:shortest=1", "-c:v", "libx264", "-pix_fmt", "yuv420p", marked)

	c := ffmpeg.New(false)
	none, err := watermark.DetectFile(ctx, c, plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("found %+v in a video without a logo", none)
	}
	got, err := watermark.DetectFile(ctx, c, marked)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("logo not found")
	}
	r := got[0]
	logo := domain.WatermarkRegion{X: float64(640-120-24) / 640, Y: 24.0 / 360, W: 120.0 / 640, H: 40.0 / 360}
	if r.X > logo.X || r.Y > logo.Y || r.X+r.W < logo.X+logo.W || r.Y+r.H < logo.Y+logo.H {
		t.Fatalf("region %+v does not cover the logo %+v", r, logo)
	}
	if r.W*r.H > 4*logo.W*logo.H {
		t.Fatalf("region %+v is far bigger than the logo %+v", r, logo)
	}
	blurred := filepath.Join(dir, "blurred.mp4")
	if err = c.BlurRegions(ctx, marked, blurred, got); err != nil {
		t.Fatal(err)
	}
	// after the blur nothing static and sharp is left: the detector must come back empty
	again, err := watermark.DetectFile(ctx, c, blurred)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("logo still detected after the blur: %+v", again)
	}
	t.Logf("logo %+v, detected %+v", logo, got)
}
