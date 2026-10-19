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

func TestBrandSmoke(t *testing.T) {
	if os.Getenv("MEDIA_SMOKE") != "1" {
		t.Skip("set MEDIA_SMOKE=1 with ffmpeg and fonts installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v %s", name, err, out)
		}
		return string(out)
	}
	source := filepath.Join(dir, "source.mp4")
	run("ffmpeg", "-y", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=540x960:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", source)
	bg := filepath.Join(dir, "bg.png")
	run("ffmpeg", "-y", "-loglevel", "error", "-f", "lavfi", "-i", "color=blue:size=640x360", "-frames:v", "1", bg)
	ass := filepath.Join(dir, "brand.ass")
	body := ("[Script Info]\nPlayResX: 1080\nPlayResY: 1920\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Liberation Sans,60,&H0000FFFF,&H0000FFFF,&H00000000,&H00000000,-1,0,0,0,100,100,0,0,1,4,2,8,40,40,1452,1\n\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:01.50,Default,,0,0,0,,Привет\\Nhello\n")
	if err := os.WriteFile(ass, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c := New(false)
	for name, bgPath := range map[string]string{"blur": "", "bg": bg} {
		out := filepath.Join(dir, name+".mp4")
		if err := c.RenderBrand(ctx, source, out, domain.BrandSpec{VideoScale: 0.75, PitchSemitones: 2, BgPath: bgPath, ASSPath: ass}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		probe := run("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,width,height,duration", "-of", "csv=p=0", out)
		if !strings.Contains(probe, "1080,1920") || !strings.Contains(probe, "audio") {
			t.Fatalf("%s: unexpected streams %q", name, probe)
		}
		if keep := os.Getenv("MEDIA_SMOKE_KEEP"); keep != "" {
			_ = os.Rename(out, filepath.Join(keep, name+".mp4"))
		}
	}
}
