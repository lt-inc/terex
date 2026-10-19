package ffmpeg

import (
	"strings"
	"testing"

	"terex/gpu-be/internal/domain"
)

func TestBrandFilterGraphBlurPinsVideoToTop(t *testing.T) {
	g := brandFilterGraph(domain.BrandSpec{VideoScale: 0.75, ASSPath: "/tmp/a:b.ass"})
	for _, want := range []string{"scale=810:1440:force_original_aspect_ratio=decrease", "boxblur=40:1", "overlay=(W-w)/2:0", `ass=/tmp/a\:b.ass`, "format=yuv420p"} {
		if !strings.Contains(g, want) {
			t.Errorf("graph lacks %q:\n%s", want, g)
		}
	}
}

func TestBrandFilterGraphBackgroundIsInputZero(t *testing.T) {
	g := brandFilterGraph(domain.BrandSpec{VideoScale: 0.75, BgPath: "/tmp/bg.mp4"})
	if !strings.HasPrefix(g, "[0:v]scale=1080:1920:force_original_aspect_ratio=increase,crop=1080:1920[bg];[1:v]scale=810:1440") || strings.Contains(g, "boxblur") || strings.Contains(g, "ass=") {
		t.Fatalf("unexpected graph:\n%s", g)
	}
}

func TestBrandAudioFilterKeepsTempo(t *testing.T) {
	if brandAudioFilter(0) != "" {
		t.Fatal("no shift = no filter")
	}
	got := brandAudioFilter(2)
	// 2^(2/12)=1.122462: pitch up by the rate, tempo back by its inverse
	if !strings.Contains(got, "asetrate=53878.") || !strings.Contains(got, "atempo=0.890899") {
		t.Fatalf("unexpected audio filter %s", got)
	}
}
