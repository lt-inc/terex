package service

import (
	"testing"

	"terex/twitch-collector/internal/domain"
)

func TestObservableSourcesSkipClipsTerexWouldReject(t *testing.T) {
	clips := []domain.Clip{
		{ID: "ok", Duration: 20},
		{ID: "limit", Duration: 60},
		{ID: "long", Duration: 60.1},
		{ID: "too-long", Duration: 180.5},
		{ID: "empty", Duration: 0},
	}
	got := observableSources(clips)
	if len(got) != 3 || got[0].ClipID != "ok" || got[1].ClipID != "limit" || got[2].ClipID != "long" {
		t.Fatalf("unexpected sources: %+v", got)
	}
}
