package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"terex/twitch-collector/internal/domain"
	"testing"
)

type fakeService struct {
	Service
	calls int
}

func (s *fakeService) Channels(context.Context) ([]domain.Channel, error) {
	s.calls++
	return []domain.Channel{}, nil
}
func (s *fakeService) Check() error { s.calls++; return nil }
func TestInternalRoutesRequireExactBearerAndHealthPublic(t *testing.T) {
	s := &fakeService{}
	h := Handler(s, "secret")
	for _, header := range []string{"", "secret", "Bearer wrong"} {
		r := httptest.NewRequest("GET", "/api/channels", nil)
		r.Header.Set("Authorization", header)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(header, w.Code)
		}
	}
	if s.calls != 0 {
		t.Fatal("unauthorized service call")
	}
	r := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("health not public")
	}
	r = httptest.NewRequest("POST", "/api/check", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 || s.calls != 1 {
		t.Fatal("check not accepted")
	}
}
func TestUnknownBodyFieldsAndMultipleObjectsRejected(t *testing.T) {
	h := Handler(&fakeService{}, "secret")
	for _, body := range []string{`{"input":"name","url":"https://evil"}`, `{"input":"name"}{"input":"other"}`, `{}`} {
		r := httptest.NewRequest(http.MethodPost, "/api/channels", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
