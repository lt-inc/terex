package ffmpeg

import (
	"context"
	"fmt"
	"math"
	"strings"

	"terex/gpu-be/internal/domain"
)

const (
	brandCanvasW = 1080
	brandCanvasH = 1920
)

// even rounds to the nearest even number: yuv420p needs even dimensions.
func even(v float64) int { return int(math.Round(v/2)) * 2 }

// brandSize is the box the shrunk video must fit.
func brandSize(scale float64) (w, h int) {
	scale = domain.BrandScale(scale)
	return even(brandCanvasW * scale), even(brandCanvasH * scale)
}

// brandFilterGraph builds the video graph; the video is input 0 without BgPath, input 1 with it.
func brandFilterGraph(spec domain.BrandSpec) string {
	w, h := brandSize(spec.VideoScale)
	fg := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", w, h)
	var g string
	if spec.BgPath != "" {
		g = fmt.Sprintf("[0:v]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d[bg];[1:v]%s[fg];", brandCanvasW, brandCanvasH, brandCanvasW, brandCanvasH, fg)
	} else {
		g = fmt.Sprintf("[0:v]split=2[f][b];[b]scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d,boxblur=40:1[bg];[f]%s[fg];", brandCanvasW, brandCanvasH, brandCanvasW, brandCanvasH, fg)
	}
	g += "[bg][fg]overlay=(W-w)/2:0,setsar=1"
	if spec.ASSPath != "" {
		esc := strings.NewReplacer(`\`, `\\`, `:`, `\:`, `'`, `\'`).Replace(spec.ASSPath)
		g += ",ass=" + esc
	}
	return g + ",format=yuv420p"
}

// brandAudioFilter shifts the pitch by semitones without changing the tempo: resample-by-rate raises pitch
// and speed together, atempo takes the speed back. Zero shift = no filter.
func brandAudioFilter(semitones float64) string {
	if semitones == 0 {
		return ""
	}
	f := math.Pow(2, semitones/12)
	return fmt.Sprintf("aresample=48000,asetrate=%.3f,aresample=48000,atempo=%.6f", 48000*f, 1/f)
}

// RenderBrand lays out the branded frame in one encode (background, video, subtitles, pitch).
func (c *Client) RenderBrand(ctx context.Context, in, out string, spec domain.BrandSpec) error {
	var args []string
	if spec.BgPath != "" {
		args = append([]string{"-stream_loop", "-1"}, c.hwaccel()...)
		args = append(args, "-i", spec.BgPath)
		args = append(args, c.hwaccel()...)
		args = append(args, "-i", in)
		args = append(args, "-filter_complex", brandFilterGraph(spec), "-map", "1:a?", "-shortest")
	} else {
		args = append(c.hwaccel(), "-i", in, "-filter_complex", brandFilterGraph(spec), "-map", "0:a?")
	}
	if af := brandAudioFilter(spec.PitchSemitones); af != "" {
		args = append(args, "-af", af)
	}
	args = append(args, c.vcodec(20, "veryfast")...)
	args = append(args, "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", out)
	return c.run(ctx, "brand", args...)
}
