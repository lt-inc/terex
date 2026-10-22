package msg

import (
	"fmt"
	"math"
)

// WatermarkMode says where the blurred areas come from.
type WatermarkMode string

const (
	// WatermarkAuto detects static overlays (logos, usernames) on every video at render time.
	WatermarkAuto WatermarkMode = "auto"
	// WatermarkManual blurs the operator's rectangles from WatermarkOptions.Regions.
	WatermarkManual WatermarkMode = "manual"
)

// MaxWatermarkRegions bounds the blur chain: every region costs one split/crop/blur/overlay in ffmpeg.
const MaxWatermarkRegions = 8

// minWatermarkSide is the smallest region side, as a share of the frame. Anything thinner is a misclick.
const minWatermarkSide = 0.005

// WatermarkRegion is a rectangle in the source frame. All values are shares of the frame (0..1), so
// the same region fits a 480p proxy, the original and a video of another resolution.
type WatermarkRegion struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// WatermarkOptions switches the watermark blur on. nil means off.
type WatermarkOptions struct {
	Mode    WatermarkMode     `json:"mode"`
	Regions []WatermarkRegion `json:"regions,omitempty"` // manual mode only
}

// Normalize validates the options and returns the canonical form: nil for "off" (an empty or "off"
// mode), regions dropped in auto mode, regions clamped into the frame in manual mode.
func (o *WatermarkOptions) Normalize() (*WatermarkOptions, error) {
	if o == nil || o.Mode == "" || o.Mode == "off" {
		return nil, nil
	}
	switch o.Mode {
	case WatermarkAuto:
		return &WatermarkOptions{Mode: WatermarkAuto}, nil
	case WatermarkManual:
		regions, err := NormalizeRegions(o.Regions)
		if err != nil {
			return nil, err
		}
		if len(regions) == 0 {
			return nil, fmt.Errorf("watermark: manual mode needs at least one region")
		}
		return &WatermarkOptions{Mode: WatermarkManual, Regions: regions}, nil
	}
	return nil, fmt.Errorf("watermark: unknown mode %q", o.Mode)
}

// NormalizeRegions clamps every region into the frame and rejects non-finite or degenerate ones.
func NormalizeRegions(in []WatermarkRegion) ([]WatermarkRegion, error) {
	if len(in) > MaxWatermarkRegions {
		return nil, fmt.Errorf("watermark: at most %d regions", MaxWatermarkRegions)
	}
	out := make([]WatermarkRegion, 0, len(in))
	for _, r := range in {
		for _, v := range []float64{r.X, r.Y, r.W, r.H} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("watermark: region has a non-finite value")
			}
		}
		x, w := clampSpan(r.X, r.W)
		y, h := clampSpan(r.Y, r.H)
		if w < minWatermarkSide || h < minWatermarkSide {
			return nil, fmt.Errorf("watermark: region is too small or outside the frame")
		}
		out = append(out, WatermarkRegion{X: x, Y: y, W: w, H: h})
	}
	return out, nil
}

// clampSpan cuts the span [start, start+size] to [0, 1]. A span already inside keeps its exact values:
// recomputing the size as end-start would turn 0.1 into 0.10000000000000002.
func clampSpan(start, size float64) (float64, float64) {
	if start < 0 {
		size += start
		start = 0
	}
	start = math.Min(start, 1)
	return start, math.Min(size, 1-start)
}
