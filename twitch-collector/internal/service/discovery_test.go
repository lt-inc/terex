package service

import (
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"testing"
	"time"
)

func TestDiscoveryEqualWindowsSubdivisionAndDurableRestart(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	start := now.Add(-100 * time.Hour)
	windows := equalDiscoveryWindows(start, now)
	if len(windows) != 13 {
		t.Fatal(len(windows))
	}
	for n, w := range windows {
		if w.End.Sub(w.Start) > 8*time.Hour || w.End.Sub(w.Start) < 7*time.Hour {
			t.Fatal(w)
		}
		if n > 0 && !windows[n-1].Start.Equal(w.End) {
			t.Fatal("gap between windows")
		}
	}
	if !windows[0].End.Equal(now) || !windows[len(windows)-1].Start.Equal(start) {
		t.Fatal("wrong range")
	}
	p := twitch.DiscoveryProgress{Epoch: 1, FlowVersion: 2, AsOf: now, Pending: []twitch.DiscoveryWindow{{Start: now.Add(-8 * time.Hour), End: now, Pages: 9, Cursor: "cap"}}, Total: 1}
	advanceDiscovery(&p, domain.ClipPage{Count: 100, LastViews: 100, Cursor: ""}, 10)
	if len(p.Pending) != 2 || p.Total != 2 || p.Pending[0].Cursor != "" {
		t.Fatal("1000-result API cap did not split window", p)
	}
	continued := prepareDiscoveryProgress(p, 1, 2, start, now.Add(time.Minute))
	if !continued.AsOf.Equal(now) || len(continued.Pending) != 2 {
		t.Fatal("incomplete run was replaced", continued)
	}
	restarted := prepareDiscoveryProgress(p, 2, 2, start, now.Add(time.Minute))
	if restarted.Epoch != 2 || len(restarted.Pending) != 13 {
		t.Fatal("reset epoch reused old cursors", restarted)
	}
	changed := prepareDiscoveryProgress(p, 1, 3, start, now.Add(time.Minute))
	if changed.FlowVersion != 3 || len(changed.Pending) != 13 {
		t.Fatal("flow edit reused old cursors", changed)
	}
	minimum := twitch.DiscoveryProgress{Pending: []twitch.DiscoveryWindow{{Start: now.Add(-15 * time.Minute), End: now, Pages: 9}}, Total: 1}
	advanceDiscovery(&minimum, domain.ClipPage{Count: 100, LastViews: 100}, 10)
	if !minimum.Truncated || len(minimum.Pending) != 0 {
		t.Fatal("minimum dense window not bounded", minimum)
	}
	if !repeatedDiscoveryCursor(twitch.DiscoveryWindow{Cursor: "B", SeenCursors: []string{"A"}}, "A") || repeatedDiscoveryCursor(twitch.DiscoveryWindow{Cursor: "B", SeenCursors: []string{"A"}}, "C") {
		t.Fatal("cursor cycle was not detected")
	}
}
