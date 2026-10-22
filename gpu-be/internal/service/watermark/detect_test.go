package watermark

import (
	"context"
	"errors"
	"math/rand"
	"testing"

	"terex/gpu-be/internal/domain"
)

const (
	tw, th = 360, 640
)

// background is a smooth random field, different in every frame: the "moving picture".
func background(rng *rand.Rand) []byte {
	const gx, gy = 7, 11
	var grid [gy + 1][gx + 1]float64
	for y := range grid {
		for x := range grid[y] {
			grid[y][x] = 40 + rng.Float64()*170
		}
	}
	pix := make([]byte, tw*th)
	for y := 0; y < th; y++ {
		for x := 0; x < tw; x++ {
			fx, fy := float64(x)*gx/tw, float64(y)*gy/th
			ix, iy := int(fx), int(fy)
			ax, ay := fx-float64(ix), fy-float64(iy)
			v := grid[iy][ix]*(1-ax)*(1-ay) + grid[iy][ix+1]*ax*(1-ay) + grid[iy+1][ix]*(1-ax)*ay + grid[iy+1][ix+1]*ax*ay
			pix[y*tw+x] = byte(v + rng.Float64()*6 - 3)
		}
	}
	return pix
}

// stamp draws a striped "logo" (a stand-in for text) with the given opacity.
func stamp(pix []byte, x0, y0, w, h int, alpha float64) {
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			logo := 20.0
			if (x/3)%2 == 0 {
				logo = 240
			}
			i := y*tw + x
			pix[i] = byte(float64(pix[i])*(1-alpha) + logo*alpha)
		}
	}
}

func frames(seed int64, n int, edit func(pix []byte)) []domain.GrayFrame {
	rng := rand.New(rand.NewSource(seed))
	out := make([]domain.GrayFrame, n)
	for i := range out {
		pix := background(rng)
		if edit != nil {
			edit(pix)
		}
		out[i] = domain.GrayFrame{W: tw, H: th, Pix: pix}
	}
	return out
}

func contains(r domain.WatermarkRegion, x0, y0, w, h int) bool {
	return r.X*tw <= float64(x0) && r.Y*th <= float64(y0) && (r.X+r.W)*tw >= float64(x0+w) && (r.Y+r.H)*th >= float64(y0+h)
}

func TestDetectFindsLogo(t *testing.T) {
	cases := []struct {
		name          string
		x, y, w, h    int
		alpha         float64
		maxAreaFactor float64 // the region may be at most this many times the logo
	}{
		{"opaque top right", 250, 24, 90, 28, 0.9, 3},
		{"translucent bottom left", 16, 580, 100, 30, 0.35, 3},
		{"small corner mark", 300, 590, 44, 24, 0.8, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Detect(frames(1, SampleFrames, func(p []byte) { stamp(p, c.x, c.y, c.w, c.h, c.alpha) }))
			if len(got) == 0 {
				t.Fatal("no region found")
			}
			if !contains(got[0], c.x, c.y, c.w, c.h) {
				t.Fatalf("region %+v does not cover the logo at %d,%d %dx%d", got[0], c.x, c.y, c.w, c.h)
			}
			if area := got[0].W * tw * got[0].H * th; area > c.maxAreaFactor*float64(c.w*c.h) {
				t.Fatalf("region %+v is %.1fx the logo, want at most %.1fx", got[0], area/float64(c.w*c.h), c.maxAreaFactor)
			}
		})
	}
}

func TestDetectToleratesLogoHiddenInSomeFrames(t *testing.T) {
	i := 0
	got := Detect(frames(2, SampleFrames, func(p []byte) {
		i++
		if i%7 != 0 { // 2 of 16 frames come without the logo, as after a cut or a fade
			stamp(p, 250, 24, 90, 28, 0.9)
		}
	}))
	if len(got) == 0 || !contains(got[0], 250, 24, 90, 28) {
		t.Fatalf("got %+v, want the logo despite two frames without it", got)
	}
}

func TestDetectFindsTwoLogos(t *testing.T) {
	got := Detect(frames(3, SampleFrames, func(p []byte) {
		stamp(p, 250, 24, 90, 28, 0.9)
		stamp(p, 20, 590, 80, 26, 0.9)
	}))
	var top, bottom bool
	for _, r := range got {
		top = top || contains(r, 250, 24, 90, 28)
		bottom = bottom || contains(r, 20, 590, 80, 26)
	}
	if !top || !bottom {
		t.Fatalf("got %+v, want both logos", got)
	}
}

func TestDetectFindsNothingWithoutLogo(t *testing.T) {
	for seed := int64(10); seed < 15; seed++ {
		if got := Detect(frames(seed, SampleFrames, nil)); len(got) != 0 {
			t.Fatalf("seed %d: found %+v in a video without overlays", seed, got)
		}
	}
}

func TestDetectIgnoresLetterbox(t *testing.T) {
	bars := func(p []byte) {
		for y := 0; y < th; y++ {
			if y < 90 || y >= th-90 {
				for x := 0; x < tw; x++ {
					p[y*tw+x] = 0
				}
			}
		}
	}
	if got := Detect(frames(4, SampleFrames, bars)); len(got) != 0 {
		t.Fatalf("found %+v: black bars are not a watermark", got)
	}
}

func TestDetectIgnoresStillPicture(t *testing.T) {
	still := frames(5, 1, func(p []byte) { stamp(p, 250, 24, 90, 28, 0.9) })[0]
	all := make([]domain.GrayFrame, SampleFrames)
	for i := range all {
		all[i] = still
	}
	if got := Detect(all); len(got) != 0 {
		t.Fatalf("found %+v in a still picture: its scene edges cannot be told from an overlay", got)
	}
}

func TestDetectIgnoresCentreOfFrame(t *testing.T) {
	if got := Detect(frames(6, SampleFrames, func(p []byte) { stamp(p, 135, 300, 90, 40, 0.9) })); len(got) != 0 {
		t.Fatalf("found %+v in the middle of the frame", got)
	}
}

func TestDetectRejectsBadInput(t *testing.T) {
	if got := Detect(nil); got != nil {
		t.Fatalf("got %+v for no frames", got)
	}
	few := frames(7, minFrames-1, func(p []byte) { stamp(p, 250, 24, 90, 28, 0.9) })
	if got := Detect(few); got != nil {
		t.Fatalf("got %+v for %d frames: too few to judge persistence", got, len(few))
	}
	mixed := frames(8, SampleFrames, nil)
	mixed[3] = domain.GrayFrame{W: 10, H: 10, Pix: make([]byte, 100)}
	if got := Detect(mixed); got != nil {
		t.Fatalf("got %+v for frames of different sizes", got)
	}
}

type fakeMedia struct {
	frames []domain.GrayFrame
	err    error
	asked  int
}

func (f *fakeMedia) GrayFrames(_ context.Context, _ string, n int) ([]domain.GrayFrame, error) {
	f.asked = n
	return f.frames, f.err
}

func TestDetectFile(t *testing.T) {
	m := &fakeMedia{frames: frames(9, SampleFrames, func(p []byte) { stamp(p, 250, 24, 90, 28, 0.9) })}
	got, err := DetectFile(context.Background(), m, "in.mp4")
	if err != nil || len(got) == 0 || m.asked != SampleFrames {
		t.Fatalf("got %+v, err %v, asked %d frames", got, err, m.asked)
	}
	if _, err = DetectFile(context.Background(), &fakeMedia{err: errors.New("boom")}, "in.mp4"); err == nil {
		t.Fatal("a media error must surface")
	}
}
