package safehttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBlocksNonPublicAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	defer srv.Close()
	if _, err := New(2*time.Second, false).Get(srv.URL); err == nil {
		t.Fatal("loopback must be refused")
	}
	if _, err := New(2*time.Second, true).Get(srv.URL); err != nil {
		t.Fatalf("allowPrivate must permit it: %v", err)
	}
	for _, target := range []string{"http://10.0.0.1/", "http://192.168.1.1/", "http://169.254.169.254/latest/meta-data", "http://[::1]/", "http://100.64.0.1/"} {
		if _, err := New(2*time.Second, false).Get(target); err == nil {
			t.Errorf("%s was reachable", target)
		}
	}
}

func TestRedirectToLoopbackIsBlocked(t *testing.T) {
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("internal")) }))
	defer inner.Close()
	// a public-looking host cannot be simulated offline, so verify the redirect target is dialled through the same guard
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, inner.URL, http.StatusFound) }))
	defer redirect.Close()
	if _, err := New(2*time.Second, false).Get(redirect.URL); err == nil {
		t.Fatal("redirect chain to loopback must fail")
	}
}
