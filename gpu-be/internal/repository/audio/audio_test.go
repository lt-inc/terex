package audio

import (
	"math"
	"testing"
)

func TestRMSDB(t *testing.T) {
	if got := rmsDB(make([]int16, 100)); got != silenceDB {
		t.Fatalf("silence = %v", got)
	}
	full := make([]int16, 100)
	for i := range full {
		full[i] = 32767
	}
	if got := rmsDB(full); math.Abs(got) > 0.01 {
		t.Fatalf("full scale should be ~0 dBFS, got %v", got)
	}
	half := make([]int16, 100)
	for i := range half {
		half[i] = 16384
	}
	if got := rmsDB(half); math.Abs(got+6.02) > 0.05 {
		t.Fatalf("half scale should be ~-6 dBFS, got %v", got)
	}
}
