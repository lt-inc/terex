package images

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPexelsSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" || r.Header.Get("Authorization") != "key123" || r.URL.Query().Get("query") != "stock market" || r.URL.Query().Get("locale") != "en-US" || r.URL.Query().Get("orientation") != "landscape" {
			t.Errorf("unexpected request %s auth=%q", r.URL.String(), r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"photos":[{"url":"https://www.pexels.com/photo/1/","photographer":"Ann","alt":"traders","src":{"large":"https://images.pexels.com/1-large.jpg","medium":"https://images.pexels.com/1-med.jpg"}},{"url":"x","photographer":"No src","src":{}}]}`))
	}))
	defer srv.Close()
	p := NewPexels("key123", srv.URL)
	photos, err := p.Search(context.Background(), "stock market", "en", 5)
	if err != nil || len(photos) != 1 || photos[0].URL != "https://images.pexels.com/1-large.jpg" || photos[0].Credit != "Ann" || photos[0].Source != "pexels" || photos[0].Thumb != "https://images.pexels.com/1-med.jpg" {
		t.Fatalf("%v %+v", err, photos)
	}
	if _, err = NewPexels("", "").Search(context.Background(), "x", "ru", 3); err != ErrNotConfigured {
		t.Fatalf("a missing key must be reported: %v", err)
	}
	if _, err = p.Search(context.Background(), "  ", "ru", 3); err == nil {
		t.Fatal("an empty query must be rejected")
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
		case "/head-refused.png":
			if r.Method == http.MethodHead {
				w.WriteHeader(405)
				return
			}
			w.Header().Set("Content-Type", "image/png")
		case "/page.html":
			w.Header().Set("Content-Type", "text/html")
		case "/huge.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", "9999999")
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &Checker{HTTP: &http.Client{Timeout: 3 * time.Second}}
	for path, wantErr := range map[string]string{"/ok.jpg": "", "/head-refused.png": "", "/page.html": "not an image", "/huge.jpg": "5 MB", "/missing.jpg": "HTTP 404"} {
		err := c.Check(context.Background(), srv.URL+path)
		if wantErr == "" && err != nil || wantErr != "" && (err == nil || !strings.Contains(err.Error(), wantErr)) {
			t.Errorf("%s: %v (want %q)", path, err, wantErr)
		}
	}
	if err := c.Check(context.Background(), "ftp://x/y.jpg"); err == nil {
		t.Error("only http(s) URLs are images")
	}
}
