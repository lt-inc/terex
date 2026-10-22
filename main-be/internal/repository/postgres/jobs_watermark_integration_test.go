package postgres

import (
	"context"
	"reflect"
	"testing"

	"terex/main-be/internal/domain"
	"terex/proto/msg"
)

// The watermark settings go into jobs.watermark and come back in the GPU command unchanged.
func TestJobWatermarkRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	manual := &msg.WatermarkOptions{Mode: msg.WatermarkManual, Regions: []msg.WatermarkRegion{{X: 0.7, Y: 0.05, W: 0.25, H: 0.1}, {X: 0.02, Y: 0.9, W: 0.2, H: 0.08}}}
	cases := map[string]*msg.WatermarkOptions{
		"wm-none":   nil,
		"wm-auto":   {Mode: msg.WatermarkAuto},
		"wm-manual": manual,
	}
	for id, want := range cases {
		err := s.CreateJob(ctx, domain.NewJob{JobID: id, YoutubeURL: "https://www.youtube.com/watch?v=test", SliceMode: "full", ApproveMode: domain.ApproveBy, MinVideoDuration: 60, Watermark: want})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		got, err := s.JobProcessMessage(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if !reflect.DeepEqual(got.Options.Watermark, want) {
			t.Fatalf("%s: GPU command carries %+v, want %+v", id, got.Options.Watermark, want)
		}
	}
}
