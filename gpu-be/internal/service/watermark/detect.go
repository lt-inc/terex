// Package watermark finds static overlays (channel logos, @usernames) in a video.
//
// The idea: scene content changes between frames spread over the video, an overlay does not. So an edge
// that is present in (almost) every sampled frame is either an overlay or part of a static scene. The
// detector keeps persistent edges, joins the pieces of one logo or one line of text into blobs, and
// keeps the blobs that look like an overlay: compact, away from the middle of the frame, with enough edges.
package watermark

import (
	"context"
	"fmt"
	"math"
	"sort"

	"terex/gpu-be/internal/domain"
)

const (
	// SampleFrames is how many stills the detector looks at. Persistence is judged across them.
	SampleFrames = 16

	minFrames = 6
	// minMotion is the mean frame-to-frame luma change below which the video is a still picture:
	// everything in it is "persistent", so nothing can be told apart as an overlay.
	minMotion = 2.0
	// persistence picks the percentile of an edge's strength over the frames. 0.2 means an edge must be
	// strong in about 80% of the frames, so a logo hidden by a fade or a cut does not vanish.
	persistence = 0.2
	// minEdge is the floor of the edge threshold (|dx|+|dy| of 8-bit luma); above it the threshold
	// follows the video: mean + edgeSigmas*std of the persistent-edge map.
	minEdge    = 8.0
	edgeSigmas = 2.5

	dilateShare = 0.025 // blob-joining radius, as a share of the frame width
	minDilate   = 3

	minMassShare = 0.0002 // fewest edge pixels in a blob, as a share of the frame area
	minMass      = 40
	minFill      = 0.04 // edge pixels / bounding box area: lower is scattered noise, not a logo
	minAreaShare = 0.0004
	maxAreaShare = 0.12
	maxWidthShr  = 0.7
	maxHeightShr = 0.4
	minThickness = 6 // px of the 360-wide working frame
	padShare     = 0.15
	maxRegions   = 3
)

// Media is the part of the media client the detector needs.
type Media interface {
	GrayFrames(ctx context.Context, in string, n int) ([]domain.GrayFrame, error)
}

// DetectFile samples SampleFrames stills of the video and returns the regions to blur.
func DetectFile(ctx context.Context, m Media, in string) ([]domain.WatermarkRegion, error) {
	frames, err := m.GrayFrames(ctx, in, SampleFrames)
	if err != nil {
		return nil, fmt.Errorf("watermark detect: %w", err)
	}
	return Detect(frames), nil
}

type blob struct {
	x0, y0, x1, y1 int // bounding box of the edge pixels, x1/y1 exclusive
	mass           int
	sum            float64
}

func (b blob) w() int { return b.x1 - b.x0 }
func (b blob) h() int { return b.y1 - b.y0 }

// Detect returns up to maxRegions regions (shares of the frame) that look like a static overlay,
// strongest first. No regions is a normal answer: most videos have no watermark.
func Detect(frames []domain.GrayFrame) []domain.WatermarkRegion {
	if len(frames) < minFrames {
		return nil
	}
	w, h := frames[0].W, frames[0].H
	if w < 32 || h < 32 {
		return nil
	}
	for _, f := range frames {
		if f.W != w || f.H != h || len(f.Pix) != w*h {
			return nil
		}
	}
	if motion(frames) < minMotion {
		return nil
	}
	edge := persistentEdges(frames, w, h)
	mean, std := meanStd(edge)
	threshold := math.Max(minEdge, mean+edgeSigmas*std)
	mask := make([]bool, w*h)
	for i, e := range edge {
		mask[i] = float64(e) >= threshold
	}
	blobs := components(mask, dilate(mask, w, h, max(minDilate, int(dilateShare*float64(w)))), edge, w, h)

	var kept []blob
	for _, b := range blobs {
		if plausible(b, w, h) {
			kept = append(kept, b)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].sum > kept[j].sum })
	var out []domain.WatermarkRegion
	var boxes [][4]float64
	for _, b := range kept {
		r := padded(b, w, h)
		box := [4]float64{r.X, r.Y, r.X + r.W, r.Y + r.H}
		overlaps := false
		for _, o := range boxes {
			if iou(box, o) > 0.3 {
				overlaps = true
				break
			}
		}
		if overlaps {
			continue
		}
		boxes = append(boxes, box)
		out = append(out, r)
		if len(out) == maxRegions {
			break
		}
	}
	return out
}

// motion is the mean absolute luma change between consecutive frames.
func motion(frames []domain.GrayFrame) float64 {
	var total float64
	for i := 1; i < len(frames); i++ {
		var s int
		a, b := frames[i-1].Pix, frames[i].Pix
		for j := range a {
			d := int(a[j]) - int(b[j])
			if d < 0 {
				d = -d
			}
			s += d
		}
		total += float64(s) / float64(len(a))
	}
	return total / float64(len(frames)-1)
}

// persistentEdges is, per pixel, the persistence-th percentile over the frames of the edge strength
// |dx|+|dy|. Moving content has weak edges in some frames and drops out; an overlay stays.
func persistentEdges(frames []domain.GrayFrame, w, h int) []uint8 {
	grads := make([][]uint8, len(frames))
	for k, f := range frames {
		g := make([]uint8, w*h)
		for y := 0; y < h-1; y++ {
			for x := 0; x < w-1; x++ {
				i := y*w + x
				dx := int(f.Pix[i+1]) - int(f.Pix[i])
				dy := int(f.Pix[i+w]) - int(f.Pix[i])
				if dx < 0 {
					dx = -dx
				}
				if dy < 0 {
					dy = -dy
				}
				g[i] = uint8(min(255, dx+dy))
			}
		}
		grads[k] = g
	}
	q := int(persistence * float64(len(frames)))
	out := make([]uint8, w*h)
	buf := make([]uint8, len(frames))
	for i := range out {
		for k := range grads {
			buf[k] = grads[k][i]
		}
		out[i] = nth(buf, q)
	}
	return out
}

// nth returns the q-th smallest value (0-based); buf is reordered. The slices are a few elements long,
// so an insertion sort beats anything cleverer.
func nth(buf []uint8, q int) uint8 {
	for i := 1; i < len(buf); i++ {
		v := buf[i]
		j := i - 1
		for ; j >= 0 && buf[j] > v; j-- {
			buf[j+1] = buf[j]
		}
		buf[j+1] = v
	}
	return buf[q]
}

func meanStd(v []uint8) (mean, std float64) {
	var s, s2 float64
	for _, x := range v {
		f := float64(x)
		s += f
		s2 += f * f
	}
	n := float64(len(v))
	mean = s / n
	return mean, math.Sqrt(math.Max(0, s2/n-mean*mean))
}

// dilate grows the mask by r pixels in every direction (a square structuring element, two 1-D passes),
// so the strokes of one logo or one line of text fall into one blob.
func dilate(mask []bool, w, h, r int) []bool {
	horizontal := make([]bool, len(mask))
	prefix := make([]int, max(w, h)+1)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			prefix[x+1] = prefix[x]
			if mask[y*w+x] {
				prefix[x+1]++
			}
		}
		for x := 0; x < w; x++ {
			horizontal[y*w+x] = prefix[min(w, x+r+1)]-prefix[max(0, x-r)] > 0
		}
	}
	out := make([]bool, len(mask))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			prefix[y+1] = prefix[y]
			if horizontal[y*w+x] {
				prefix[y+1]++
			}
		}
		for y := 0; y < h; y++ {
			out[y*w+x] = prefix[min(h, y+r+1)]-prefix[max(0, y-r)] > 0
		}
	}
	return out
}

// components labels the 4-connected blobs of the dilated mask and measures, for each, the original
// edge pixels it holds (the bounding box comes from those, not from the dilated halo).
func components(mask, dilated []bool, edge []uint8, w, h int) []blob {
	seen := make([]bool, len(dilated))
	var blobs []blob
	var stack []int
	for start := range dilated {
		if !dilated[start] || seen[start] {
			continue
		}
		b := blob{x0: w, y0: h}
		stack = append(stack[:0], start)
		seen[start] = true
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := i%w, i/w
			if mask[i] {
				b.mass++
				b.sum += float64(edge[i])
				b.x0, b.y0 = min(b.x0, x), min(b.y0, y)
				b.x1, b.y1 = max(b.x1, x+1), max(b.y1, y+1)
			}
			for _, n := range [4]int{i - 1, i + 1, i - w, i + w} {
				nx, ny := n%w, n/w
				if n < 0 || n >= len(dilated) || !dilated[n] || seen[n] || abs(nx-x)+abs(ny-y) != 1 {
					continue
				}
				seen[n] = true
				stack = append(stack, n)
			}
		}
		if b.mass > 0 {
			blobs = append(blobs, b)
		}
	}
	return blobs
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// plausible is the overlay filter: compact, solid enough, not a bar across the frame, not in the middle
// (a watermark sits near an edge or a corner; the middle belongs to the picture).
func plausible(b blob, w, h int) bool {
	area := float64(w * h)
	box := float64(b.w() * b.h())
	switch {
	case b.mass < max(minMass, int(minMassShare*area)):
		return false
	case b.w() < minThickness || b.h() < minThickness:
		return false
	case box < minAreaShare*area || box > maxAreaShare*area:
		return false
	case float64(b.w()) > maxWidthShr*float64(w) || float64(b.h()) > maxHeightShr*float64(h):
		return false
	case float64(b.mass)/box < minFill:
		return false
	}
	cx, cy := float64(b.x0+b.x1)/2/float64(w), float64(b.y0+b.y1)/2/float64(h)
	return cx < 0.3 || cx > 0.7 || cy < 0.3 || cy > 0.7
}

// padded widens the blob's box, because the detected edges are thinner than the overlay's soft borders
// and shadows, and returns it as a region.
func padded(b blob, w, h int) domain.WatermarkRegion {
	px, py := padShare*float64(b.w())+2, padShare*float64(b.h())+2
	x0, y0 := math.Max(0, float64(b.x0)-px), math.Max(0, float64(b.y0)-py)
	x1, y1 := math.Min(float64(w), float64(b.x1)+px), math.Min(float64(h), float64(b.y1)+py)
	return domain.WatermarkRegion{X: x0 / float64(w), Y: y0 / float64(h), W: (x1 - x0) / float64(w), H: (y1 - y0) / float64(h)}
}

func iou(a, b [4]float64) float64 {
	ix := math.Max(0, math.Min(a[2], b[2])-math.Max(a[0], b[0]))
	iy := math.Max(0, math.Min(a[3], b[3])-math.Max(a[1], b[1]))
	inter := ix * iy
	union := (a[2]-a[0])*(a[3]-a[1]) + (b[2]-b[0])*(b[3]-b[1]) - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}
