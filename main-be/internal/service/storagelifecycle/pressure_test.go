package storagelifecycle

import (
	"strings"
	"testing"
)

func TestPressureSamplesFailClosed(t *testing.T) {
	valid := `{"status":"success","data":{"resultType":"vector","result":[{"value":[1791331200,"1073741824"]}]}}`
	if n, err := parsePressureValue(strings.NewReader(valid)); err != nil || n != 1<<30 {
		t.Fatalf("valid physical bytes: %d %v", n, err)
	}
	for _, sample := range []string{
		`{"status":"success","data":{"resultType":"vector","result":[]}}`,
		`{"status":"success","data":{"resultType":"vector","result":[{"value":[1791331200,"NaN"]}]}}`,
	} {
		if _, err := parsePressureValue(strings.NewReader(sample)); err == nil {
			t.Fatal("unknown storage sample was accepted", sample)
		}
	}
}
