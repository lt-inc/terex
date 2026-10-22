package renderer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"terex/gpu-be/internal/domain"
	"terex/proto/msg"
	"terex/proto/workflow"
)

// watermarkMedia adds the optional watermark capability to the recording test media.
type watermarkMedia struct {
	testMedia
	regions []domain.WatermarkRegion
	frames  []domain.GrayFrame
}

func (m *watermarkMedia) BlurRegions(_ context.Context, in, out string, r []domain.WatermarkRegion) error {
	m.regions = r
	return m.copy("blur", in, out)
}
func (m *watermarkMedia) GrayFrames(context.Context, string, int) ([]domain.GrayFrame, error) {
	m.calls = append(m.calls, "frames")
	return m.frames, nil
}
func (m *watermarkMedia) StillFrames(context.Context, string, string, int) ([]domain.StillFrame, error) {
	return nil, nil
}

// flashing frames: the whole picture changes between frames (motion), optionally with a static striped logo.
func flashing(logo bool) []domain.GrayFrame {
	const w, h = 360, 640
	out := make([]domain.GrayFrame, 16)
	for k := range out {
		pix := make([]byte, w*h)
		for i := range pix {
			pix[i] = byte(30 + k*12)
		}
		if logo {
			for y := 24; y < 52; y++ {
				for x := 250; x < 340; x++ {
					v := byte(20)
					if (x/3)%2 == 0 {
						v = 240
					}
					pix[y*w+x] = v
				}
			}
		}
		out[k] = domain.GrayFrame{W: w, H: h, Pix: pix}
	}
	return out
}

func scratch(t *testing.T) (dir, in string) {
	t.Helper()
	dir = t.TempDir()
	in = filepath.Join(dir, "src.mp4")
	if err := os.WriteFile(in, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, in
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestMaskWatermarkOffPassesThrough(t *testing.T) {
	media := &watermarkMedia{}
	dir, in := scratch(t)
	for _, o := range []*domain.WatermarkOptions{nil, {}, {Mode: "off"}} {
		got, err := (&Service{Media: media}).maskWatermark(context.Background(), dir, in, o)
		if err != nil || got != in || len(media.calls) != 0 {
			t.Fatalf("options %+v: got %q, err %v, calls %v", o, got, err, media.calls)
		}
	}
}

func TestMaskWatermarkManualBlursTheDrawnRegions(t *testing.T) {
	media := &watermarkMedia{}
	dir, in := scratch(t)
	region := domain.WatermarkRegion{X: 0.7, Y: 0.05, W: 0.25, H: 0.1}
	got, err := (&Service{Media: media}).maskWatermark(context.Background(), dir, in, &domain.WatermarkOptions{Mode: msg.WatermarkManual, Regions: []domain.WatermarkRegion{region}})
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "unmarked.mp4") || strings.Join(media.calls, ",") != "blur" {
		t.Fatalf("got %q, calls %v", got, media.calls)
	}
	if len(media.regions) != 1 || media.regions[0] != region {
		t.Fatalf("blurred %+v, want %+v", media.regions, region)
	}
	if exists(in) || !exists(got) {
		t.Fatal("the unblurred copy must be freed and the blurred one kept")
	}
}

func TestMaskWatermarkAutoBlursWhatItFinds(t *testing.T) {
	media := &watermarkMedia{frames: flashing(true)}
	dir, in := scratch(t)
	got, err := (&Service{Media: media}).maskWatermark(context.Background(), dir, in, &domain.WatermarkOptions{Mode: msg.WatermarkAuto})
	if err != nil {
		t.Fatal(err)
	}
	if got == in || strings.Join(media.calls, ",") != "frames,blur" || len(media.regions) == 0 {
		t.Fatalf("got %q, calls %v, regions %+v", got, media.calls, media.regions)
	}
	r := media.regions[0]
	if r.X*360 > 250 || r.Y*640 > 24 || (r.X+r.W)*360 < 340 || (r.Y+r.H)*640 < 52 {
		t.Fatalf("region %+v does not cover the logo", r)
	}
}

func TestMaskWatermarkAutoLeavesCleanVideoAlone(t *testing.T) {
	media := &watermarkMedia{frames: flashing(false)}
	dir, in := scratch(t)
	got, err := (&Service{Media: media}).maskWatermark(context.Background(), dir, in, &domain.WatermarkOptions{Mode: msg.WatermarkAuto})
	if err != nil || got != in || strings.Join(media.calls, ",") != "frames" || !exists(in) {
		t.Fatalf("got %q, err %v, calls %v", got, err, media.calls)
	}
}

func TestMaskWatermarkFailsWhenItCannotBlur(t *testing.T) {
	dir, in := scratch(t)
	manual := &domain.WatermarkOptions{Mode: msg.WatermarkManual, Regions: []domain.WatermarkRegion{{X: 0.1, Y: 0.1, W: 0.2, H: 0.1}}}
	if _, err := (&Service{Media: &testMedia{}}).maskWatermark(context.Background(), dir, in, manual); err == nil {
		t.Fatal("media without the capability must fail the job, not publish the watermark")
	}
	media := &watermarkMedia{}
	if _, err := (&Service{Media: media}).maskWatermark(context.Background(), dir, in, &domain.WatermarkOptions{Mode: msg.WatermarkManual}); err == nil || len(media.calls) != 0 {
		t.Fatalf("manual mode without regions must fail before any ffmpeg call, err %v, calls %v", err, media.calls)
	}
}

func TestRevisionBlursAfterTheCutAndSkipsTheFastPath(t *testing.T) {
	media := &watermarkMedia{}
	s := Service{Store: &testStore{}, Media: media, TmpDir: t.TempDir(), TwitchFastPath: true}
	region := domain.WatermarkRegion{X: 0.7, Y: 0.05, W: 0.25, H: 0.1}
	w := workflow.RenderWork{Twitch: true, StartSec: 0.25, EndSec: 1.5, Source: workflow.SourceManifest{
		Original: domain.ObjectRef{Bucket: "imports", Object: "source"}, DurationSec: 2,
		Options: domain.JobOptions{Vertical: true, Shape: "rectangle", Watermark: &domain.WatermarkOptions{Mode: msg.WatermarkManual, Regions: []domain.WatermarkRegion{region}}},
	}, Output: domain.ObjectRef{Bucket: "clips", Object: "attempt"}}
	if _, err := s.RenderRevision(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(media.calls, ","); got != "cut,blur,vertical" {
		t.Fatalf("calls %q, want the cut first so only the clip is blurred, then the legacy transforms", got)
	}
}
