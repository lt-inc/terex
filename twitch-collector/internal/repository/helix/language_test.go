package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
)

func TestLanguageSearchDoesNotTrustChannelHintOrStopAtEnglishPage(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/clips":
			pages++
			if r.URL.Query().Get("language") != "" {
				t.Error("Get Clips has no language query parameter")
			}
			if r.URL.Query().Get("after") == "" {
				fmt.Fprint(w, `{"data":[{"id":"English","broadcaster_id":"42","game_id":"509658","language":"en","created_at":"2026-10-07T00:00:00Z","duration":20,"view_count":1000},{"id":"Unknown","broadcaster_id":"42","game_id":"509658","created_at":"2026-10-07T00:00:00Z","duration":20,"view_count":500}],"pagination":{"cursor":"next"}}`)
			} else {
				fmt.Fprint(w, `{"data":[{"id":"Russian","broadcaster_id":"42","game_id":"509658","language":"ru","created_at":"2026-10-07T00:00:00Z","duration":20,"view_count":100}]}`)
			}
		case "/channels":
			fmt.Fprint(w, `{"data":[{"broadcaster_id":"42","broadcaster_login":"streamer","broadcaster_language":"ru"}]}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	c := New("app", "secret")
	c.API, c.token, c.expires = server.URL, "cached-app-token", time.Now().Add(time.Hour)
	end := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	flow := twitch.Flow{ID: "ru", SourceMode: "category", CategoryID: "509658", Language: "ru", MinViews: 10, MinDurationSec: 5, MaxDurationSec: 60}
	clips, err := c.GetFlowClips(context.Background(), flow, domain.Channel{}, end.Add(-72*time.Hour), end, 1, nil)
	if err != nil || pages != 2 || len(clips) != 3 {
		t.Fatalf("pages=%d clips=%+v err=%v", pages, clips, err)
	}
	if clips[0].Language != "en" || clips[0].LanguageSource != "clip" || clips[1].Language != "ru" || clips[1].LanguageSource != "channel" || clips[2].LanguageSource != "clip" {
		t.Fatalf("language provenance lost: %+v", clips)
	}
	reports := c.DiscoveryReports()
	if len(reports) != 1 || reports[0].LanguageMatched != 1 || reports[0].LanguageRejected != 1 || reports[0].LanguageUnknown != 1 || reports[0].PageLimitReached {
		t.Fatalf("incorrect discovery report: %+v", reports)
	}
}
