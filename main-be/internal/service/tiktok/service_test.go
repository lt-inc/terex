package tiktok

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"terex/main-be/internal/domain"
	"testing"
)

type fakeRepo struct {
	Repository
	account domain.Account
	saved   []domain.TikTokConnection
}

func (f *fakeRepo) GetAccount(context.Context, int64) (domain.Account, error) {
	return f.account, nil
}
func (f *fakeRepo) SaveTikTokConnection(_ context.Context, c domain.TikTokConnection) error {
	f.saved = append(f.saved, c)
	return nil
}

type uploader struct {
	mu       sync.Mutex
	calls    []string
	status   string
	helpCode int
	helpBody string
	pairing  map[string]any
}

func (u *uploader) serve(t *testing.T) *Service {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.calls = append(u.calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/connections/7":
			_, _ = w.Write([]byte(`{"status":"` + u.status + `"}`))
		case r.Method == "POST" && r.URL.Path == "/v1/connections":
			_ = json.NewDecoder(r.Body).Decode(&u.pairing)
			_, _ = w.Write([]byte(`{"status":"pairing"}`))
		case r.URL.Path == "/v1/connections/7/browser-help":
			if u.helpCode != 0 {
				w.WriteHeader(u.helpCode)
				_, _ = w.Write([]byte(u.helpBody))
				return
			}
			_, _ = w.Write([]byte(`{"status":"challenge","help_url":"https://uploader.invalid/api/tiktok/browser-help/secret/"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return &Service{Repo: &fakeRepo{account: domain.Account{Platform: "tiktok", RemoteAccountID: ptr("remote-42")}}, URL: srv.URL, Token: "svc", AppURL: "https://app.invalid"}
}

func ptr(s string) *string { return &s }

func TestBrowserHelpInitializesDisconnectedAccountFirst(t *testing.T) {
	u := &uploader{status: "disconnected"}
	s := u.serve(t)
	out, err := s.Connection(context.Background(), 7, "browser-help")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /v1/connections/7", "POST /v1/connections", "POST /v1/connections/7/browser-help"}
	if strings.Join(u.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", u.calls, want)
	}
	if u.pairing["expected_remote_account_id"] != "remote-42" || u.pairing["account_id"] != float64(7) {
		t.Fatalf("pairing identity not preserved: %v", u.pairing)
	}
	if out.PairingToken != "" || out.ExpiresAt != nil || out.PayloadURL != "" {
		t.Fatalf("pairing material leaked: %+v", out)
	}
	if out.Status != "challenge" || out.HelpURL == "" || out.AccountID != 7 {
		t.Fatalf("unexpected help response: status=%s account=%d", out.Status, out.AccountID)
	}
	if len(s.Repo.(*fakeRepo).saved) != 0 {
		t.Fatal("browser help must not persist the pairing state")
	}
}

func TestBrowserHelpDoesNotReinitializeExistingState(t *testing.T) {
	for _, status := range []string{"connected", "pairing", "needs_reauth"} {
		u := &uploader{status: status}
		s := u.serve(t)
		if _, err := s.Connection(context.Background(), 7, "browser-help"); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		want := "GET /v1/connections/7,POST /v1/connections/7/browser-help"
		if strings.Join(u.calls, ",") != want {
			t.Fatalf("%s: calls = %v", status, u.calls)
		}
	}
}

func TestBrowserHelpSkippedWhenInitializationFails(t *testing.T) {
	u := &uploader{status: "disconnected"}
	s := u.serve(t)
	s.Repo = &fakeRepo{account: domain.Account{Platform: "youtube"}}
	if _, err := s.Connection(context.Background(), 7, "browser-help"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	if len(u.calls) != 0 {
		t.Fatalf("uploader called: %v", u.calls)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.calls = append(u.calls, r.Method+" "+r.URL.Path)
		u.mu.Unlock()
		if r.Method == "GET" {
			_, _ = w.Write([]byte(`{"status":"disconnected"}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	s = &Service{Repo: &fakeRepo{account: domain.Account{Platform: "tiktok"}}, URL: failing.URL, Token: "svc"}
	u.calls = nil
	if _, err := s.Connection(context.Background(), 7, "browser-help"); err == nil {
		t.Fatal("expected bootstrap failure")
	}
	for _, c := range u.calls {
		if strings.Contains(c, "browser-help") {
			t.Fatalf("browser help requested after failed init: %v", u.calls)
		}
	}
}

func TestBrowserHelpWhitelistsErrors(t *testing.T) {
	cases := map[string]string{
		"Connect/export the account before opening browser help": "не инициализирован",
		"Another operator browser is open; close it first":       "другого оператора",
		`{"input":{"cookies":[{"value":"secret-cookie"}]}}`:      "проверьте код подключения",
	}
	for detail, fragment := range cases {
		body, _ := json.Marshal(map[string]string{"detail": detail})
		u := &uploader{status: "connected", helpCode: http.StatusBadRequest, helpBody: string(body)}
		s := u.serve(t)
		_, err := s.Connection(context.Background(), 7, "browser-help")
		if !errors.Is(err, domain.ErrInvalid) || !strings.Contains(err.Error(), fragment) {
			t.Fatalf("detail %q: err = %v", detail, err)
		}
		if strings.Contains(err.Error(), "secret-cookie") || strings.Contains(err.Error(), detail) {
			t.Fatalf("upstream body relayed: %v", err)
		}
	}
}
