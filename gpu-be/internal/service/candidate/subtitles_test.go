package candidate

import (
	"strings"
	"testing"

	"terex/gpu-be/internal/domain"
)

func TestGroupSegmentsMergesShortTail(t *testing.T) {
	segs := []domain.Segment{
		{Start: 0, End: 30, Text: "a"}, {Start: 30, End: 65, Text: "b"}, // group 0: 65s
		{Start: 65, End: 80, Text: "c"}, // short tail -> merged into group 0
	}
	got := GroupSegments(segs, 60)
	if len(got) != 1 || got[0].End != 80 || got[0].ID != 0 {
		t.Fatalf("unexpected groups: %+v", got)
	}
}

func TestGroupSegmentsSequentialIDs(t *testing.T) {
	segs := []domain.Segment{{Start: 0, End: 61}, {Start: 61, End: 130}, {Start: 130, End: 200}}
	got := GroupSegments(segs, 60)
	if len(got) != 3 || got[2].ID != 2 {
		t.Fatalf("unexpected groups: %+v", got)
	}
}

func TestSRTFormat(t *testing.T) {
	out := SRT([]domain.Segment{{Start: 65.23, End: 66, Text: " hi "}})
	if !strings.Contains(out, "00:01:05,230 --> 00:01:06,000\nhi\n") {
		t.Fatalf("bad srt: %q", out)
	}
}
