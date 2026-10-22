package msg

import (
	"math"
	"testing"
)

func TestWatermarkNormalize(t *testing.T) {
	region := WatermarkRegion{X: 0.7, Y: 0.05, W: 0.25, H: 0.1}
	cases := []struct {
		name    string
		in      *WatermarkOptions
		want    *WatermarkOptions
		wantErr bool
	}{
		{name: "nil is off"},
		{name: "empty mode is off", in: &WatermarkOptions{Regions: []WatermarkRegion{region}}},
		{name: "off drops regions", in: &WatermarkOptions{Mode: "off", Regions: []WatermarkRegion{region}}},
		{name: "auto drops regions", in: &WatermarkOptions{Mode: WatermarkAuto, Regions: []WatermarkRegion{region}}, want: &WatermarkOptions{Mode: WatermarkAuto}},
		{name: "manual keeps regions", in: &WatermarkOptions{Mode: WatermarkManual, Regions: []WatermarkRegion{region}}, want: &WatermarkOptions{Mode: WatermarkManual, Regions: []WatermarkRegion{region}}},
		{name: "manual clamps into the frame", in: &WatermarkOptions{Mode: WatermarkManual, Regions: []WatermarkRegion{{X: 0.9, Y: -0.1, W: 0.5, H: 0.3}}},
			want: &WatermarkOptions{Mode: WatermarkManual, Regions: []WatermarkRegion{{X: 0.9, Y: 0, W: 0.1, H: 0.2}}}},
		{name: "manual without regions", in: &WatermarkOptions{Mode: WatermarkManual}, wantErr: true},
		{name: "unknown mode", in: &WatermarkOptions{Mode: "blur"}, wantErr: true},
		{name: "region outside the frame", in: &WatermarkOptions{Mode: WatermarkManual, Regions: []WatermarkRegion{{X: 1.2, Y: 0.1, W: 0.1, H: 0.1}}}, wantErr: true},
		{name: "region too thin", in: &WatermarkOptions{Mode: WatermarkManual, Regions: []WatermarkRegion{{X: 0.1, Y: 0.1, W: 0.001, H: 0.1}}}, wantErr: true},
		{name: "NaN", in: &WatermarkOptions{Mode: WatermarkManual, Regions: []WatermarkRegion{{X: math.NaN(), Y: 0.1, W: 0.1, H: 0.1}}}, wantErr: true},
		{name: "too many regions", in: &WatermarkOptions{Mode: WatermarkManual, Regions: make([]WatermarkRegion, MaxWatermarkRegions+1)}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.in.Normalize()
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if (got == nil) != (c.want == nil) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			if got == nil {
				return
			}
			if got.Mode != c.want.Mode || len(got.Regions) != len(c.want.Regions) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			for i, r := range got.Regions {
				w := c.want.Regions[i]
				if math.Abs(r.X-w.X)+math.Abs(r.Y-w.Y)+math.Abs(r.W-w.W)+math.Abs(r.H-w.H) > 1e-9 {
					t.Fatalf("region %d = %+v, want %+v", i, r, w)
				}
			}
		})
	}
}
