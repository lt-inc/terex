package helix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"terex/twitch-collector/internal/domain"
	"testing"
	"time"
)

func TestChannelInputNeverRequestsArbitraryURL(t *testing.T) {
	for _, s := range []string{"https://evil.example/user", "https://twitch.tv.evil.example/user", "https://user@twitch.tv/name", "https://twitch.tv/name/clip/test", "../secret", "https://twitch.tv/name?redirect=evil"} {
		if _, err := Login(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	for _, s := range []string{"Some_Name", "https://www.twitch.tv/Some_Name/"} {
		v, err := Login(s)
		if err != nil || v != "some_name" {
			t.Fatalf("%q: %s %v", s, v, err)
		}
	}
}
func TestAppTokenCachedAndMetadataBounded(t *testing.T) {
	var tokens atomic.Int32
	var unauthorized atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.Method != "POST" {
				t.Error("wrong token method")
			}
			r.ParseForm()
			if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_secret") != "secret" || r.Form.Get("scope") != "" {
				t.Error("wrong application token contract")
			}
			tokens.Add(1)
			fmt.Fprint(w, `{"access_token":"app-token","expires_in":3600}`)
		case "/users":
			if !unauthorized.Swap(true) {
				w.WriteHeader(401)
				return
			}
			if r.Header.Get("Authorization") != "Bearer app-token" || r.Header.Get("Client-Id") != "app-id" {
				t.Error("missing authorization")
			}
			fmt.Fprint(w, `{"data":[{"id":"42","login":"name","display_name":"Name"}]}`)
		case "/clips":
			q := r.URL.Query()
			if q.Get("first") != "100" || q.Get("broadcaster_id") != "42" || q.Get("started_at") == "" || q.Get("after") != "" {
				t.Error("unbounded/wrong discovery request")
			}
			fmt.Fprint(w, `{"data":[{"id":"Safe-Clip","broadcaster_id":"42","created_at":"2026-10-04T00:00:00Z","duration":20,"view_count":100,"video_id":"v","vod_offset":12},{"id":"../evil","broadcaster_id":"42"}]}`)
		default:
			t.Error("unexpected path")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := New("app-id", "secret")
	c.API = server.URL
	c.OAuth = server.URL + "/token"
	ch, err := c.ResolveChannel(context.Background(), "name")
	if err != nil || ch.BroadcasterID != "42" {
		t.Fatal(ch, err)
	}
	clips, err := c.GetClips(context.Background(), domain.Channel{ID: "local", BroadcasterID: "42", Login: "name"}, time.Now().Add(-72*time.Hour), time.Now(), 100)
	if err != nil || len(clips) != 1 || clips[0].URL != "https://clips.twitch.tv/Safe-Clip" {
		t.Fatal(clips, err)
	}
	if tokens.Load() != 2 {
		t.Fatalf("tokens=%d; expected 401 renewal then caching", tokens.Load())
	}
}
