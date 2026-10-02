package jobs

import (
	"testing"
	"time"

	"terex/main-be/internal/domain"
)

func TestParseScheduleUsesUTC7(t *testing.T) {
	got := ParseSchedule(domain.PublishSchedule{Date: "2026-01-02", Time: "10:30"})
	want := time.Date(2026, 1, 2, 3, 30, 0, 0, time.UTC)
	if got == nil || !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if ParseSchedule(domain.PublishSchedule{Date: "2026-01-02"}) != nil {
		t.Fatal("incomplete schedule must be nil")
	}
}

func TestStepAcceptsNumberAndString(t *testing.T) {
	if step(domain.PublishSchedule{Step: float64(15)}) != 15 || step(domain.PublishSchedule{Step: " 20 "}) != 20 || step(domain.PublishSchedule{}) != 0 {
		t.Fatal("bad step parsing")
	}
}
