package service

import (
	"math"
	"reflect"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"testing"
	"time"
)

func ptr(v float64) *float64 { return &v }
func TestSelectionFiltersAndFairOrder(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)
	s := twitch.DefaultSettings()
	base := func(id, ch string, views int) domain.Clip {
		return domain.Clip{ID: id, ChannelID: ch, ViewCount: views, CreatedAt: now.Add(-2 * time.Hour), Duration: 20}
	}
	clips := []domain.Clip{base("a1", "a", 1000), base("a2", "a", 900), base("a3", "a", 800), base("b1", "b", 200), base("b2", "b", 100), base("a1", "a", 1000)}
	invalid := base("recent", "c", 1000)
	invalid.CreatedAt = now.Add(-59 * time.Minute)
	clips = append(clips, invalid)
	invalid = base("old", "c", 1000)
	invalid.CreatedAt = now.Add(-73 * time.Hour)
	clips = append(clips, invalid)
	invalid = base("short", "c", 1000)
	invalid.Duration = 4
	clips = append(clips, invalid)
	invalid = base("nan", "c", 1000)
	invalid.Duration = math.NaN()
	clips = append(clips, invalid)
	invalid = base("seen", "c", 1000)
	invalid.State = "imported"
	clips = append(clips, invalid)
	invalid = base("low", "c", 99)
	clips = append(clips, invalid)
	chosen := Select(clips, s, now)
	var ids []string
	for _, c := range chosen {
		ids = append(ids, c.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a1", "b1", "a2", "b2", "a3"}) {
		t.Fatalf("got %v", ids)
	}
}
func TestAnyKnownVODIntersection(t *testing.T) {
	a := domain.Clip{VideoID: "v", Duration: 20, VODOffset: ptr(100)}
	b := a
	b.VODOffset = ptr(119.9)
	if !Overlaps(a, b) {
		t.Fatal("partial overlap missed")
	}
	b.VODOffset = ptr(120)
	if Overlaps(a, b) {
		t.Fatal("touching boundary is not overlap")
	}
	b.VODOffset = nil
	if Overlaps(a, b) {
		t.Fatal("unknown offsets are not evidence")
	}
	a.VideoID = ""
	b.VideoID = ""
	b.VODOffset = ptr(100)
	if Overlaps(a, b) {
		t.Fatal("missing VOD is not evidence")
	}
}
func TestNetworkRetryBudget(t *testing.T) {
	now := time.Now()
	f := &domain.Failure{Retryable: true}
	for attempt, delay := range map[int]time.Duration{1: 5 * time.Minute, 2: 30 * time.Minute} {
		at := RetryAt(attempt, f, now)
		if at == nil || at.Sub(now) != delay {
			t.Fatalf("attempt %d: %v", attempt, at)
		}
	}
	if RetryAt(3, f, now) != nil {
		t.Fatal("fourth attempt would be scheduled")
	}
	if RetryAt(1, &domain.Failure{Kind: "unavailable"}, now) != nil {
		t.Fatal("permanent error retried")
	}
	f.RetryAfter = time.Hour
	if RetryAt(1, f, now).Sub(now) != time.Hour {
		t.Fatal("server cooldown ignored")
	}
}
