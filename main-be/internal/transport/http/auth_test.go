package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOperatorAuthCapabilities(t *testing.T) {
	a := &OperatorAuth{Username: "operator", Password: "test-password", ServiceToken: "service-test", AllowedOrigin: "http://localhost:3000"}
	h := a.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, c := range []struct {
		method, path, auth, origin string
		want                       int
	}{
		{"GET", "/api/twitch/settings", "Bearer service-test", "", 204},
		{"PUT", "/api/twitch/settings", "Bearer service-test", "", 403},
		{"POST", "/api/imports", "Bearer service-test", "", 204},
		{"POST", "/api/imports/id/complete", "Bearer service-test", "", 204},
		{"POST", "/api/clips/id/approve", "Bearer service-test", "", 403},
		{"GET", "/api/accounts/list", "Bearer service-test", "", 403},
		{"GET", "/api/jobs", "", "", 401},
		{"GET", "/api/accounts/oauth/callback", "", "", 204},
		{"POST", "/api/accounts/oauth/callback", "", "", 401},
		{"POST", "/api/jobs", "basic", "https://attacker.invalid", 403},
		{"GET", "/api/jobs", "basic", "", 204},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Header.Set("Authorization", c.auth)
		r.Header.Set("Origin", c.origin)
		if c.auth == "basic" {
			r.SetBasicAuth("operator", "test-password")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s %s: %d want %d", c.method, c.path, w.Code, c.want)
		}
	}
}
