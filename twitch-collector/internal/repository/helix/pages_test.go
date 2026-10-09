package helix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"terex/proto/twitch"
	"terex/twitch-collector/internal/domain"
	"testing"
	"time"
)

func TestWindowPageDoesNotAdvanceWithoutRequiredChannelMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/clips":
			fmt.Fprint(w, `{"data":[{"id":"Clip","broadcaster_id":"42","broadcaster_name":"Привет Мир","created_at":"2026-10-09T00:00:00Z","duration":20,"view_count":500,"language":"ru"}]}`)
		case "/channels":
			fmt.Fprint(w, `{"data":[{"broadcaster_id":"42","broadcaster_login":"actual_login","broadcaster_language":"ru"}]}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c := New("id", "secret")
	c.API = server.URL
	c.token = "cached"
	c.expires = time.Now().Add(time.Hour)
	c.DiscoveryMaxRequests = 1
	c.DiscoveryRPS = 1000000
	f := twitch.Flow{ID: "flow", SourceMode: "category", CategoryID: "1"}
	window := twitch.DiscoveryWindow{Start: time.Now().Add(-8 * time.Hour), End: time.Now()}
	_, err := c.GetClipPage(c.BeginDiscoveryCycle(context.Background()), f, domain.Channel{}, window)
	var failure *domain.Failure
	if !errors.As(err, &failure) || failure.Code != "discovery_budget" {
		t.Fatal("silently skipped clip after metadata budget failure", err)
	}
	c.DiscoveryMaxRequests = 2
	page, err := c.GetClipPage(c.BeginDiscoveryCycle(context.Background()), f, domain.Channel{}, window)
	if err != nil || len(page.Clips) != 1 || page.Clips[0].BroadcasterName != "actual_login" {
		t.Fatal(page, err)
	}
}
