package ffmpeg

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"terex/gpu-be/internal/domain"
)

const (
	// grayFrameWidth is the detector's working width: wide enough for a username in the corner to keep
	// its edges, small enough that 16 frames cost a few MB.
	grayFrameWidth = 360
	// stillFrameMaxWidth caps the stills shown in the region editor.
	stillFrameMaxWidth = 960
)

// pixelRegion is a blur area in whole pixels, aligned to even coordinates (yuv420p chroma).
type pixelRegion struct{ X, Y, W, H int }

// regionPixels converts a share-of-frame region to pixels, rounding outwards to even numbers so the
// whole marked area is covered, and keeps it inside a width x height frame.
func regionPixels(width, height int, r domain.WatermarkRegion) pixelRegion {
	// eps keeps float noise (0.05+0.1 = 0.15000000000000002) from growing the region by a pixel
	const eps = 1e-6
	x0 := int(math.Floor(r.X*float64(width)+eps)) &^ 1
	y0 := int(math.Floor(r.Y*float64(height)+eps)) &^ 1
	x1 := (int(math.Ceil((r.X+r.W)*float64(width)-eps)) + 1) &^ 1
	y1 := (int(math.Ceil((r.Y+r.H)*float64(height)-eps)) + 1) &^ 1
	x1, y1 = min(x1, width), min(y1, height)
	// a region at the very edge of an odd-sized frame can end up one pixel wide: grow it inwards
	if x1-x0 < 2 {
		x0 = max(0, x1-2)
	}
	if y1-y0 < 2 {
		y0 = max(0, y1-2)
	}
	return pixelRegion{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// blurSigma makes text inside the region unreadable whatever its size: about a third of the short side.
func blurSigma(p pixelRegion) int { return max(10, min(p.W, p.H)/3) }

// watermarkBlurGraph blurs each region of the width x height frame in turn: split, crop the area,
// blur the copy, overlay it back. The result is labelled [vout].
func watermarkBlurGraph(width, height int, regions []domain.WatermarkRegion) string {
	var b strings.Builder
	cur := "0:v"
	for i, r := range regions {
		p := regionPixels(width, height, r)
		fmt.Fprintf(&b, "[%s]split=2[a%d][c%d];[c%d]crop=%d:%d:%d:%d,gblur=sigma=%d[b%d];[a%d][b%d]overlay=%d:%d[o%d];",
			cur, i, i, i, p.W, p.H, p.X, p.Y, blurSigma(p), i, i, i, p.X, p.Y, i)
		cur = fmt.Sprintf("o%d", i)
	}
	fmt.Fprintf(&b, "[%s]format=yuv420p[vout]", cur)
	return b.String()
}

// BlurRegions blurs the regions in every frame of in; audio is copied as it is.
func (c *Client) BlurRegions(ctx context.Context, in, out string, regions []domain.WatermarkRegion) error {
	if len(regions) == 0 {
		return fmt.Errorf("blur: no regions")
	}
	p, err := c.Probe(ctx, in)
	if err != nil {
		return fmt.Errorf("blur: %w", err)
	}
	if p.Width <= 0 || p.Height <= 0 {
		return fmt.Errorf("blur: source has no video stream")
	}
	args := append(c.hwaccel(), "-i", in, "-filter_complex", watermarkBlurGraph(p.Width, p.Height, regions), "-map", "[vout]", "-map", "0:a?")
	args = append(args, c.vcodec(18, "veryfast")...)
	args = append(args, "-c:a", "copy", "-movflags", "+faststart", out)
	return c.run(ctx, "watermark_blur", args...)
}

// sampleTimes spreads n timestamps over the middle 90% of the video: the first and last seconds are
// often an intro, an outro or a fade, which is no place to look for a watermark.
func sampleTimes(duration float64, n int) []float64 {
	times := make([]float64, n)
	for i := range times {
		times[i] = duration * (0.05 + 0.9*(float64(i)+0.5)/float64(n))
	}
	return times
}

// scaledHeight is the even height of a width x height frame scaled to newWidth.
func scaledHeight(width, height, newWidth int) int {
	return max(2, int(math.Round(float64(height)*float64(newWidth)/float64(width)/2))*2)
}

// GrayFrames takes n luma stills at grayFrameWidth, spread over the video.
func (c *Client) GrayFrames(ctx context.Context, in string, n int) ([]domain.GrayFrame, error) {
	p, err := c.Probe(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("frames: %w", err)
	}
	if p.Width <= 0 || p.Height <= 0 || p.DurationSec <= 0 {
		return nil, fmt.Errorf("frames: source has no video stream")
	}
	w := min(grayFrameWidth, p.Width)
	h := scaledHeight(p.Width, p.Height, w)
	dir, err := os.MkdirTemp("", "wm-gray-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	var frames []domain.GrayFrame
	for i, t := range sampleTimes(p.DurationSec, n) {
		raw := filepath.Join(dir, fmt.Sprintf("f%02d.raw", i))
		if err = c.run(ctx, "watermark_frame", "-ss", strconv.FormatFloat(t, 'f', 3, 64), "-i", in, "-frames:v", "1",
			"-vf", fmt.Sprintf("scale=%d:%d,format=gray", w, h), "-f", "rawvideo", raw); err != nil {
			return nil, err
		}
		pix, err := os.ReadFile(raw)
		// a seek past the last decodable frame writes nothing: skip it rather than fail the whole sample
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(pix) != w*h {
			continue
		}
		frames = append(frames, domain.GrayFrame{W: w, H: h, Pix: pix})
	}
	return frames, nil
}

// StillFrames takes n JPEG stills (at most stillFrameMaxWidth wide), spread over the video.
func (c *Client) StillFrames(ctx context.Context, in, dir string, n int) ([]domain.StillFrame, error) {
	p, err := c.Probe(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("frames: %w", err)
	}
	if p.Width <= 0 || p.Height <= 0 || p.DurationSec <= 0 {
		return nil, fmt.Errorf("frames: source has no video stream")
	}
	w := min(stillFrameMaxWidth, p.Width)
	h := scaledHeight(p.Width, p.Height, w)
	var frames []domain.StillFrame
	for i, t := range sampleTimes(p.DurationSec, n) {
		file := filepath.Join(dir, fmt.Sprintf("still%02d.jpg", i))
		if err = c.run(ctx, "watermark_still", "-ss", strconv.FormatFloat(t, 'f', 3, 64), "-i", in, "-frames:v", "1",
			"-vf", fmt.Sprintf("scale=%d:%d", w, h), "-q:v", "4", file); err != nil {
			return nil, err
		}
		jpg, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(jpg) == 0 {
			continue
		}
		frames = append(frames, domain.StillFrame{TimeSec: t, JPEG: jpg})
	}
	return frames, nil
}
