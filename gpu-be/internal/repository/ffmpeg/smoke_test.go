package ffmpeg

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
)

func TestMediaSmoke(t *testing.T) {
	if os.Getenv("MEDIA_SMOKE") != "1" {
		t.Skip("set MEDIA_SMOKE=1 with ffmpeg and fonts installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.CommandContext(ctx, "ffmpeg", append([]string{"-y", "-hide_banner", "-loglevel", "error"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg failed: %v %s", err, out)
		}
	}
	source := filepath.Join(dir, "source.mp4")
	run("-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "1", source)
	banner := filepath.Join(dir, "banner.png")
	run("-f", "lavfi", "-i", "color=red:size=100x50", "-frames:v", "1", "-threads", "1", banner)
	videoBanner := filepath.Join(dir, "banner.mp4")
	run("-f", "lavfi", "-i", "color=magenta:size=320x240:rate=25", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=44100", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", videoBanner)
	c := New(false)
	cut := filepath.Join(dir, "cut.mp4")
	if err := c.Cut(ctx, source, cut, 0.25, 1.5); err != nil {
		t.Fatal(err)
	}
	vertical := filepath.Join(dir, "vertical.mp4")
	if err := c.Vertical(ctx, cut, vertical, domain.VerticalSpec{Width: 1080, Blur: 10}); err != nil {
		t.Fatal(err)
	}
	ass := filepath.Join(dir, "subtitles.ass")
	if err := os.WriteFile(ass, []byte("[Script Info]\nScriptType: v4.00+\nPlayResX: 1080\nPlayResY: 1920\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\nStyle: Default,Liberation Sans,60,&H00FFFFFF,&H00FFFFFF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,2,10,10,20,1\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,Тест subtitles\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	subs := filepath.Join(dir, "subs.mp4")
	if err := c.BurnSubtitles(ctx, vertical, ass, subs); err != nil {
		t.Fatal(err)
	}
	imageOut := filepath.Join(dir, "image.mp4")
	if err := c.Banner(ctx, subs, banner, imageOut, false); err != nil {
		t.Fatal(err)
	}
	videoOut := filepath.Join(dir, "video.mp4")
	if err := c.Banner(ctx, cut, videoBanner, videoOut, true); err != nil {
		t.Fatal(err)
	}
	duration, err := c.Duration(ctx, imageOut)
	if err != nil || duration < 1 || duration > 1.5 {
		t.Fatalf("duration=%f err=%v", duration, err)
	}
	b, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-select_streams", "a", "-show_entries", "stream=channels", "-of", "json", videoOut).Output()
	if err != nil {
		t.Fatal(err)
	}
	var audio struct {
		Streams []struct {
			Channels int `json:"channels"`
		} `json:"streams"`
	}
	if err = json.Unmarshal(b, &audio); err != nil {
		t.Fatal(err)
	}
	if len(audio.Streams) != 1 || audio.Streams[0].Channels != 1 {
		t.Fatalf("banner audio replaced source audio: %s", b)
	}
}
