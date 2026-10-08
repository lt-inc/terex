package twitch

import (
	"testing"
	"time"

	t "terex/proto/twitch"
)

func TestValidateImportDurationLimit(tt *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	base := t.ImportRequest{SchemaVersion: 1, IdempotencyKey: "k", Source: t.Source{
		Platform: "twitch", ClipID: "Clip", BroadcasterID: "1", BroadcasterLogin: "streamer", Title: "x",
		URL: "https://clips.twitch.tv/Clip", CreatedAt: now.Add(-time.Hour), DurationSec: 20}}
	for _, c := range []struct {
		d  float64
		ok bool
	}{{20, true}, {60.1, true}, {120, true}, {t.MaxClipDurationSec, true}, {t.MaxClipDurationSec + 0.1, false}, {0, false}} {
		r := base
		r.Source.DurationSec = c.d
		if err := ValidateImport(r, now); (err == nil) != c.ok {
			tt.Errorf("duration %v: ok=%v err=%v", c.d, c.ok, err)
		}
	}
}
