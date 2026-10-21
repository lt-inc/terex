package domain

import "testing"

func TestBrandVideoHeight(t *testing.T) {
	for _, c := range []struct{ w, h, want int }{
		{1080, 1920, 1440}, // 9:16 fills the box
		{720, 1280, 1440},
		{1080, 1080, 810}, // square: width-bound
		{1920, 1080, 456}, // landscape
		{0, 0, 1440},      // unknown size = the box
	} {
		if got := BrandVideoHeight(0.75, c.w, c.h); got != c.want {
			t.Errorf("%dx%d: got %d, want %d", c.w, c.h, got, c.want)
		}
	}
}
