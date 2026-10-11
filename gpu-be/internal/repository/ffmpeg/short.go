package ffmpeg

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"terex/gpu-be/internal/domain"
)

// RenderShort decodes/accurately seeks, composes and encodes exactly once. The
// full foreground is retained; only the intentionally blurred background crops.
func (c *Client) RenderShort(ctx context.Context, s domain.ShortRenderSpec, out string) error {
	if s.Source == "" || s.StartSec < 0 || s.EndSec <= s.StartSec || math.IsNaN(s.StartSec) || math.IsNaN(s.EndSec) || math.IsInf(s.StartSec, 0) || math.IsInf(s.EndSec, 0) {
		return fmt.Errorf("invalid short bounds")
	}
	if s.Banner != "" {
		ext := strings.ToLower(filepath.Ext(s.Banner))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
			return fmt.Errorf("short fast path requires a static PNG/JPEG banner")
		}
	}
	duration := strconv.FormatFloat(s.EndSec-s.StartSec, 'f', 6, 64)
	args := []string{"-ss", strconv.FormatFloat(s.StartSec, 'f', 6, 64), "-accurate_seek"}
	args = append(args, c.hwaccel()...)
	args = append(args, "-i", s.Source)
	if s.Banner != "" {
		args = append(args, "-loop", "1", "-i", s.Banner)
	}
	graph := "[0:v]split=2[f][b];[f]scale=1080:1920:force_original_aspect_ratio=decrease:force_divisible_by=2[fg];" +
		"[b]scale=270:480:force_original_aspect_ratio=increase:force_divisible_by=2,crop=270:480,boxblur=luma_radius=10:luma_power=1:chroma_radius=5:chroma_power=1,scale=1080:1920[bg];" +
		"[bg][fg]overlay=(W-w)/2:(H-h)/2:shortest=1,setsar=1,format=yuv420p[base]"
	label := "[base]"
	if s.Banner != "" {
		// Preserve the previous static banner geometry/rounding and positioning.
		geq := `r=r(X\,Y):g=g(X\,Y):b=b(X\,Y):a=if(lte(hypot(max(0\,max(20-X\,X-(W-20)))\,max(0\,max(20-Y\,Y-(H-20))))\,20)\,alpha(X\,Y)\,0)`
		pos := "20:20"
		if s.Rectangle {
			pos = "(W-w)/2:(H-h-20)"
		}
		graph += ";[1:v][base]scale2ref=w=iw-40:h=ih/6[logo_raw][base2];[logo_raw]format=rgba,geq=" + geq + "[logo];[base2][logo]overlay=" + pos + ":shortest=1,format=yuv420p[video]"
		label = "[video]"
	}
	args = append(args, "-filter_complex", graph, "-map", label, "-map", "0:a:0?", "-t", duration)
	args = append(args, c.vcodec(20, "veryfast")...)
	args = append(args, "-c:a", "aac", "-b:a", "128k", "-pix_fmt", "yuv420p", "-fps_mode", "passthrough", "-movflags", "+faststart", out)
	return c.run(ctx, "short_single_pass", args...)
}
