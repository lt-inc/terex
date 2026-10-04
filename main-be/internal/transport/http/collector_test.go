package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	twitchservice "terex/main-be/internal/service/twitch"
	"testing"
)

func TestCollectorProxyUsesDedicatedCredentialAndKeepsStatus(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/clips" || r.URL.Query().Get("limit") != "3" || r.Header.Get("Authorization") != "Bearer collector-only" {
			t.Errorf("unexpected request %s auth=%t", r.URL.String(), r.Header.Get("Authorization") == "Bearer collector-only")
		}
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"detail":"retry later"}`))
	}))
	defer remote.Close()
	p := &CollectorProxy{URL: remote.URL, Token: "collector-only"}
	r := httptest.NewRequest("GET", "/api/twitch/clips?limit=3", nil)
	r.SetBasicAuth("operator", "password")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, r)
	if w.Code != 429 || !strings.Contains(w.Body.String(), "retry later") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestCollectorProxyRoutesMatchCollectorAPI(t *testing.T) {
	for _, tc := range []struct{ method, path string }{{"GET", "status"}, {"POST", "channels"}, {"PATCH", "channels/123"}, {"DELETE", "channels/123"}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/"+tc.path || r.Method != tc.method {
					t.Errorf("unexpected collector route %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer remote.Close()
			a := &API{Twitch: &twitchservice.Service{}, Collector: &CollectorProxy{URL: remote.URL, Token: "collector-only"}}
			w := httptest.NewRecorder()
			a.Router().ServeHTTP(w, httptest.NewRequest(tc.method, "/api/twitch/"+tc.path, strings.NewReader(`{"input":"streamer"}`)))
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestCollectorProxyRejectsUncontrolledPath(t *testing.T) {
	p := &CollectorProxy{URL: "http://unused.invalid", Token: "collector-only"}
	for _, path := range []string{"channels/123/other", "channels/..", "settings", "../status"} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("PATCH", "/api/twitch/"+path, nil))
		if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
}
func TestCollectorProxyRejectsRedirect(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "https://example.invalid", 302) }))
	defer remote.Close()
	p := &CollectorProxy{URL: remote.URL, Token: "collector-only"}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("POST", "/api/twitch/check", strings.NewReader(`{}`)))
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
}
