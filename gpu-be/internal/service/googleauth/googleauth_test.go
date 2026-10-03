package googleauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/youtube"
)

type memoryRepo struct {
	mu sync.Mutex
	c  domain.GoogleCredential
}

func (r *memoryRepo) Get(context.Context, string) (domain.GoogleCredential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.c, nil
}
func (r *memoryRepo) Put(_ context.Context, c domain.GoogleCredential) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.c = c
	return nil
}
func (r *memoryRepo) Delete(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.c = domain.GoogleCredential{}
	return nil
}

func TestConcurrentRefreshIsSerializedAndPreservesRefreshToken(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "old-refresh" || r.Form.Get("client_secret") != "client-secret" {
			t.Error("wrong refresh request")
		}
		_, _ = w.Write([]byte(`{"access_token":"new-token","expires_in":3600}`))
	}))
	defer server.Close()
	repo := &memoryRepo{c: domain.GoogleCredential{AccountID: "1", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Hour)}}
	s := New(repo, "client-id", "client-secret", "https://app.example/callback")
	s.TokenURL = server.URL
	s.HTTP = server.Client()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := s.AccessToken(context.Background(), "1")
			if err != nil || token != "new-token" {
				t.Errorf("token %q err %v", token, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || repo.c.RefreshToken != "old-refresh" {
		t.Fatalf("calls=%d credential=%+v", calls.Load(), repo.c)
	}
}

func TestOAuthPKCEStateSingleUseAndChannelBinding(t *testing.T) {
	repo := &memoryRepo{c: domain.GoogleCredential{AccountID: "1", RefreshToken: "old-refresh", ChannelID: "original-channel"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			if len(r.Form.Get("code_verifier")) < 43 || r.Form.Get("redirect_uri") != "https://app.example/callback" {
				t.Error("missing PKCE or wrong redirect")
			}
			_, _ = fmt.Fprintf(w, `{"access_token":"access","expires_in":3600,"scope":%q}`, Scope)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"other-channel","snippet":{"title":"Other"}}]}`))
	}))
	defer server.Close()
	s := New(repo, "client", "secret", "https://app.example/callback")
	s.TokenURL = server.URL + "/token"
	s.HTTP = server.Client()
	s.YouTube = func(token youtube.TokenSource) *youtube.Client {
		c := youtube.New(token)
		c.APIURL = server.URL
		c.HTTP = server.Client()
		return c
	}
	u, state, err := s.Begin("1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(u)
	if parsed.Query().Get("code_challenge_method") != "S256" || parsed.Query().Get("access_type") != "offline" || parsed.Query().Get("state") != state {
		t.Fatal("OAuth URL missing safeguards")
	}
	_, err = s.ExchangeVerified(context.Background(), "1", state, "code", "original-channel")
	if err == nil {
		t.Fatal("wrong channel accepted")
	}
	if repo.c.ChannelID != "original-channel" {
		t.Fatal("wrong channel overwrote existing credential")
	}
	if _, err = s.Exchange(context.Background(), state, "code"); err == nil {
		t.Fatal("state reused")
	}
}

func TestInvalidGrantRequiresReconnect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()
	s := New(&memoryRepo{c: domain.GoogleCredential{RefreshToken: "revoked"}}, "id", "secret", "https://app.example/callback")
	s.TokenURL = server.URL
	s.HTTP = server.Client()
	_, err := s.AccessToken(context.Background(), "1")
	if !errors.Is(err, domain.ErrReauthentication) {
		t.Fatalf("err=%v", err)
	}
}
