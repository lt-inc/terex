package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"terex/main-be/internal/domain"
	tiktokservice "terex/main-be/internal/service/tiktok"
)

type tikTokProxyRepo struct {
	tiktokservice.Repository
	platform string
	saved    []domain.TikTokConnection
}

func (r *tikTokProxyRepo) GetAccount(_ context.Context, id int64) (domain.Account, error) {
	return domain.Account{ID: id, Platform: r.platform}, nil
}

func (r *tikTokProxyRepo) SaveTikTokConnection(_ context.Context, connection domain.TikTokConnection) error {
	r.saved = append(r.saved, connection)
	return nil
}

func proxyTestAPI(repo *tikTokProxyRepo, uploaderURL string) *API {
	return &API{
		Auth:   &OperatorAuth{Username: "operator", Password: "test-password", ServiceToken: "collector-token", AllowedOrigin: "https://terex.example"},
		TikTok: &tiktokservice.Service{Repo: repo, URL: uploaderURL, Token: "uploader-token"},
	}
}

func TestTikTokProxyForwardsSettingsAndPersistsFenceWithoutReturningSecrets(t *testing.T) {
	const secretURL = "socks5://proxy-user:proxy-secret@proxy.example:1080"
	for _, tc := range []struct {
		name, body, mode string
		wantURL          bool
	}{
		{"custom", `{"mode":"custom","url":"` + secretURL + `"}`, "custom", true},
		{"reuse saved custom", `{"mode":"custom"}`, "custom", false},
		{"inherit global", `{"mode":"default"}`, "default", false},
		{"disable", `{"mode":"direct"}`, "direct", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := 0
			uploader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called++
				if r.Method != http.MethodPut || r.URL.Path != "/v1/connections/7/proxy" || r.Header.Get("Authorization") != "Bearer uploader-token" {
					t.Error("unexpected uploader request", r.Method, r.URL.Path)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				_, hasURL := payload["url"]
				if payload["mode"] != tc.mode || hasURL != tc.wantURL || (hasURL && payload["url"] != secretURL) {
					t.Error("proxy settings were not forwarded correctly")
				}
				// Even unexpected upstream credential fields must not be forwarded
				// to the operator or added to the DB connection record.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"account_id": 7, "status": "needs_reauth", "session_version": 8,
					"remote_account_id": "123456789", "session_ref": "private-session-ref",
					"proxy_mode": tc.mode, "proxy_display": "socks5://proxy.example:1080",
					"custom_proxy_configured": true, "url": secretURL, "proxy_url": secretURL,
				})
			}))
			defer uploader.Close()
			repo := &tikTokProxyRepo{platform: "tiktok"}
			api := proxyTestAPI(repo, uploader.URL)
			r := httptest.NewRequest(http.MethodPut, "/api/accounts/7/tiktok/proxy", strings.NewReader(tc.body))
			r.SetBasicAuth("operator", "test-password")
			r.Header.Set("Origin", "https://terex.example")
			out := httptest.NewRecorder()
			api.Router().ServeHTTP(out, r)
			if out.Code != http.StatusOK || called != 1 || len(repo.saved) != 1 {
				t.Fatal("proxy update failed", out.Code, called, len(repo.saved))
			}
			saved := repo.saved[0]
			if saved.AccountID != 7 || saved.Status != "needs_reauth" || saved.SessionVersion != 8 || saved.SessionRef != "private-session-ref" {
				t.Fatal("route change did not preserve the uploader session fence", saved)
			}
			storedJSON, _ := json.Marshal(saved)
			for _, secret := range []string{secretURL, "proxy-user", "proxy-secret", `"proxy_url"`, `"url"`} {
				if strings.Contains(out.Body.String(), secret) || strings.Contains(string(storedJSON), secret) {
					t.Fatal("proxy credential escaped its input-only DTO")
				}
			}
			var connection domain.TikTokConnection
			if err := json.Unmarshal(out.Body.Bytes(), &connection); err != nil {
				t.Fatal(err)
			}
			if connection.SessionRef != "" || connection.ProxyMode != tc.mode || connection.ProxyDisplay != "socks5://proxy.example:1080" || !connection.CustomProxyConfigured {
				t.Fatal("incorrect public proxy response", connection)
			}
		})
	}
}

func TestTikTokProxyRejectsUnauthorizedOrInvalidUpdatesBeforeForwarding(t *testing.T) {
	for _, tc := range []struct {
		name, auth, origin, platform, body string
		want                               int
	}{
		{"unauthenticated", "", "", "tiktok", `{"mode":"direct"}`, 401},
		{"collector", "collector", "", "tiktok", `{"mode":"direct"}`, 403},
		{"cross origin", "operator", "https://other.example", "tiktok", `{"mode":"direct"}`, 403},
		{"wrong platform", "operator", "", "youtube", `{"mode":"direct"}`, 400},
		{"invalid mode", "operator", "", "tiktok", `{"mode":"bad"}`, 400},
		{"URL without custom", "operator", "", "tiktok", `{"mode":"direct","url":"http://user:secret@proxy.example"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uploader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("rejected update reached uploader")
				w.WriteHeader(500)
			}))
			defer uploader.Close()
			repo := &tikTokProxyRepo{platform: tc.platform}
			r := httptest.NewRequest(http.MethodPut, "/api/accounts/7/tiktok/proxy", strings.NewReader(tc.body))
			if tc.auth == "operator" {
				r.SetBasicAuth("operator", "test-password")
			} else if tc.auth == "collector" {
				r.Header.Set("Authorization", "Bearer collector-token")
			}
			r.Header.Set("Origin", tc.origin)
			out := httptest.NewRecorder()
			proxyTestAPI(repo, uploader.URL).Router().ServeHTTP(out, r)
			if out.Code != tc.want || len(repo.saved) != 0 || strings.Contains(out.Body.String(), "secret@") {
				t.Fatal("invalid proxy update was not rejected safely", out.Code)
			}
		})
	}
}

func TestTikTokProxyValidationErrorDoesNotEchoUpstreamCredentialInput(t *testing.T) {
	uploader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":[{"input":"http://user:proxy-secret@proxy.example","msg":"invalid proxy"}]}`))
	}))
	defer uploader.Close()
	repo := &tikTokProxyRepo{platform: "tiktok"}
	r := httptest.NewRequest(http.MethodPut, "/api/accounts/7/tiktok/proxy", strings.NewReader(`{"mode":"custom","url":"http://user:proxy-secret@proxy.example"}`))
	r.SetBasicAuth("operator", "test-password")
	out := httptest.NewRecorder()
	proxyTestAPI(repo, uploader.URL).Router().ServeHTTP(out, r)
	if out.Code != http.StatusBadRequest || strings.Contains(out.Body.String(), "proxy-secret") || len(repo.saved) != 0 {
		t.Fatal("upstream validation leaked credentials or saved a rejected route", out.Code)
	}
}
