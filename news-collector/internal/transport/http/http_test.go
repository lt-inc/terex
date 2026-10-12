package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"terex/news-collector/internal/repository/llm"
	"terex/news-collector/internal/service"
)

func TestAuthAndHealth(t *testing.T) {
	svc := service.New(service.DefaultConfig(), nil, nil, nil, llm.New("", "", ""), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(Handler(&API{Svc: svc, Token: "s3cret-token", Log: slog.Default(), Started: time.Now()}))
	defer srv.Close()

	get := func(path, auth string) int {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if get("/health", "") != 200 {
		t.Error("health must be public")
	}
	for _, auth := range []string{"", "Bearer wrong", "s3cret-token", "Bearer "} {
		for _, path := range []string{"/api/status", "/api/flows", "/api/posts", "/api/channels"} {
			if code := get(path, auth); code != 401 {
				t.Errorf("%s with %q returned %d", path, auth, code)
			}
		}
	}
}

func TestEmptyTokenRejectsEverything(t *testing.T) {
	svc := service.New(service.DefaultConfig(), nil, nil, nil, llm.New("", "", ""), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(Handler(&API{Svc: svc, Token: "", Log: slog.Default(), Started: time.Now()}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/flows", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("an unconfigured token must not mean open access, got %d", res.StatusCode)
	}
}

func TestMetricsEndpointIsScrapableWithoutToken(t *testing.T) {
	svc := service.New(service.DefaultConfig(), nil, nil, nil, llm.New("", "", ""), nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(Handler(&API{Svc: svc, Token: "s3cret-token", Log: slog.Default(), Started: time.Now()}))
	defer srv.Close()
	res, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(body), "news_post_publish_delay_seconds") || !strings.Contains(string(body), "go_goroutines") {
		t.Fatalf("metrics: %d\n%.300s", res.StatusCode, body)
	}
	if strings.Contains(string(body), "s3cret-token") {
		t.Fatal("metrics must not leak secrets")
	}
}
