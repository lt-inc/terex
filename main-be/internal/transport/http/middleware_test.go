package http

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFailLogsOnceWithReasonAndRetryableDeadline(t *testing.T) {
	var log bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	w := httptest.NewRecorder()
	AccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fail(w, context.DeadlineExceeded) })).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/twitch/collector-config", nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("deadline response: %d %s", w.Code, w.Body)
	}
	if strings.Count(log.String(), "msg=") != 1 || !strings.Contains(log.String(), "reason_code=deadline_exceeded") || !strings.Contains(log.String(), "level=ERROR") {
		t.Fatalf("deadline logging: %s", &log)
	}
	log.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	AccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fail(w, context.Canceled) })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/test", nil).WithContext(ctx))
	if !strings.Contains(log.String(), "reason_code=request_cancelled") || !strings.Contains(log.String(), "level=WARN") {
		t.Fatalf("client cancellation logging: %s", &log)
	}
}
