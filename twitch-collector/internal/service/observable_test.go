package service

import (
	"testing"

	"terex/twitch-collector/internal/domain"
)

func TestObservableSourcesSkipClipsTerexWouldReject(t *testing.T) {
	clips := []domain.Clip{
		{ID: "ok", Duration: 20},
		{ID: "limit", Duration: 60},
		{ID: "too-long", Duration: 60.1},
		{ID: "empty", Duration: 0},
	}
	got := observableSources(clips)
	if len(got) != 2 || got[0].ClipID != "ok" || got[1].ClipID != "limit" {
		t.Fatalf("unexpected sources: %+v", got)
	}
}
