package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewsProxyForwardsWithOwnTokenAndKeepsStatus(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/posts/7/approve" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer news-only" || r.URL.Query().Get("x") != "1" {
			t.Errorf("unexpected request %s %s auth=%q", r.Method, r.URL.String(), r.Header.Get("Authorization"))
		}
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"error":"post is not in a state that allows this"}`))
	}))
	defer remote.Close()
	a := &API{News: &NewsProxy{URL: remote.URL, Token: "news-only"}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/news/posts/7/approve?x=1", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer operator-credential-must-not-leak")
	a.Router().ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "allows this") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestNewsProxyRoutes(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer remote.Close()
	p := &NewsProxy{URL: remote.URL, Token: "t"}
	for _, ok := range []string{"status", "articles", "sources", "sources/test", "sources/12", "sources/12/fetch", "channels", "channels/3/verify", "channels/3/test",
		"flows", "flows/9", "flows/9/generate", "flows/9/stories", "posts", "posts/5", "posts/5/approve", "posts/5/reject", "posts/5/preview", "posts/5/images"} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("GET", "/api/news/"+ok, nil))
		if w.Code != 200 {
			t.Errorf("%s -> %d", ok, w.Code)
		}
	}
	for _, bad := range []string{"", "../status", "flows/9/other", "flows/x", "posts/5/approve/extra", "internal", "sources/12/fetch/x", "flows/9/../../health"} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("GET", "/api/news/"+bad, nil))
		if w.Code != 404 {
			t.Errorf("%q -> %d, want 404", bad, w.Code)
		}
	}
}

func TestNewsProxyNotConfigured(t *testing.T) {
	a := &API{}
	w := httptest.NewRecorder()
	a.Router().ServeHTTP(w, httptest.NewRequest("GET", "/api/news/status", nil))
	if w.Code != 503 {
		t.Fatalf("an unconfigured collector must answer 503, got %d", w.Code)
	}
}
