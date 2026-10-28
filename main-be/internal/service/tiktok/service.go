package tiktok

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"terex/main-be/internal/domain"
	"terex/main-be/internal/service/workflow"
	w "terex/proto/workflow"
	"time"
)

type Repository interface {
	GetAccount(context.Context, int64) (domain.Account, error)
	GetPublicationRecord(context.Context, int64) (domain.PublicationRecord, error)
	SaveTikTokConnection(context.Context, domain.TikTokConnection) error
	TikTokAttemptAllowed(context.Context, string) (bool, error)
	TikTokPublicationAllowed(context.Context, int64) (bool, error)
	ReconcileTikTok(context.Context, int64, int, string, string, bool, time.Time) error
	RetryTikTokAfterAbsence(context.Context, int64, int, time.Time) error
	ReleaseTikTokOperation(context.Context, string, string, time.Time) (bool, error)
}
type MediaGateway interface {
	PresignGet(context.Context, string, string) (string, error)
}
type Service struct {
	Repo                            Repository
	Workflow                        *workflow.Service
	Gpu                             MediaGateway
	URL, Token, WorkerToken, AppURL string
	HTTP                            *http.Client
}

func (s *Service) request(ctx context.Context, method, path string, payload any) (domain.TikTokConnection, error) {
	var out domain.TikTokConnection
	if s.URL == "" || s.Token == "" {
		return out, fmt.Errorf("TikTok uploader is unavailable: configure TIKTOK_UPLOADER_URL and service token")
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return out, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("TikTok uploader unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusConflict {
			// Whitelist safe validation messages; never relay a Pydantic input
			// dump containing cookies or arbitrary upstream response bodies.
			message := "TikTok отклонил запрос: проверьте код подключения и состояние аккаунта"
			if strings.HasSuffix(path, "/proxy") {
				message = "TikTok отклонил запрос: проверьте настройки прокси"
			}
			var body struct {
				Detail string `json:"detail"`
			}
			_ = json.NewDecoder(io.LimitReader(resp.Body, 2048)).Decode(&body)
			switch body.Detail {
			case "Pairing token is expired or already used":
				message = "Код подключения TikTok истёк или уже использован. Получите новый код в Accounts"
			case "Account is not connected":
				message = "TikTok-аккаунт ещё не подключён. Передайте сессию через расширение"
			case "A different TikTok account is logged in. Connect the original account.":
				message = "В браузере открыт другой TikTok-аккаунт. Войдите в исходный аккаунт"
			case "Proxy mode must be default, direct or custom":
				message = "Выберите режим прокси default, direct или custom"
			case "A proxy address is only accepted in custom mode":
				message = "Адрес прокси допустим только для собственного прокси"
			case "A valid proxy address is required":
				message = "Укажите действительный адрес прокси"
			case "Invalid proxy address or unsupported browser proxy authentication":
				message = "Проверьте формат адреса и поддержку авторизации прокси в браузере"
			}
			kind := domain.ErrInvalid
			if resp.StatusCode == http.StatusConflict {
				kind = domain.ErrConflict
			}
			return out, fmt.Errorf("%w: %s", kind, message)
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return out, fmt.Errorf("TikTok uploader unavailable: internal service credentials rejected")
		}
		return out, fmt.Errorf("TikTok uploader request failed (HTTP %d)", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return out, fmt.Errorf("invalid TikTok uploader response")
	}
	return out, nil
}
func (s *Service) Connect(ctx context.Context, id int64) (domain.TikTokConnection, error) {
	a, err := s.Repo.GetAccount(ctx, id)
	if err != nil {
		return domain.TikTokConnection{}, err
	}
	if a.Platform != "tiktok" {
		return domain.TikTokConnection{}, domain.ErrInvalid
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return domain.TikTokConnection{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(key)
	expiry := time.Now().UTC().Add(10 * time.Minute)
	expected := ""
	if a.RemoteAccountID != nil {
		expected = *a.RemoteAccountID
	}
	out, err := s.request(ctx, "POST", "/v1/connections", map[string]any{"account_id": id, "pairing_token": token, "expires_at": expiry, "expected_remote_account_id": expected})
	if err != nil {
		return out, err
	}
	out.AccountID = id
	out.PairingToken = token
	out.ExpiresAt = &expiry
	out.PayloadURL = strings.TrimRight(s.AppURL, "/") + "/api/tiktok/connect/payload"
	return out, nil
}
func (s *Service) Connection(ctx context.Context, id int64, action string) (domain.TikTokConnection, error) {
	a, err := s.Repo.GetAccount(ctx, id)
	if err != nil {
		return domain.TikTokConnection{}, err
	}
	if a.Platform != "tiktok" {
		return domain.TikTokConnection{}, domain.ErrInvalid
	}
	path := fmt.Sprintf("/v1/connections/%d", id)
	method := "GET"
	if action == "check" {
		method = "POST"
		path += "/check"
	}
	if action == "disconnect" {
		method = "DELETE"
	}
	if action == "browser-help" {
		method = "POST"
		path += "/browser-help"
	}
	out, err := s.request(ctx, method, path, nil)
	if err != nil {
		return out, err
	}
	out.AccountID = id
	if action != "browser-help" && out.Status != "pairing" {
		err = s.Repo.SaveTikTokConnection(ctx, out)
	}
	return out, err
}
func (s *Service) Import(ctx context.Context, payload json.RawMessage) (domain.TikTokConnection, error) {
	out, err := s.request(ctx, "POST", "/v1/connect/payload", payload)
	if err != nil {
		return out, err
	}
	if out.AccountID <= 0 {
		return out, domain.ErrInvalid
	}
	if err = s.Repo.SaveTikTokConnection(ctx, out); err != nil {
		return out, err
	}
	out.PairingToken = ""
	return out, nil
}

func (s *Service) SetProxy(ctx context.Context, id int64, settings domain.TikTokProxySettings) (domain.TikTokConnection, error) {
	a, err := s.Repo.GetAccount(ctx, id)
	if err != nil {
		return domain.TikTokConnection{}, err
	}
	if a.Platform != "tiktok" {
		return domain.TikTokConnection{}, domain.ErrInvalid
	}
	switch settings.Mode {
	case "default", "direct":
		if settings.URL != nil {
			return domain.TikTokConnection{}, fmt.Errorf("%w: URL прокси допустим только в режиме custom", domain.ErrInvalid)
		}
	case "custom":
		// An omitted URL reuses the uploader's existing encrypted custom proxy.
	default:
		return domain.TikTokConnection{}, fmt.Errorf("%w: выберите режим прокси default, direct или custom", domain.ErrInvalid)
	}
	out, err := s.request(ctx, "PUT", fmt.Sprintf("/v1/connections/%d/proxy", id), settings)
	if err != nil {
		return out, err
	}
	out.AccountID = id
	// The changed route can fence a running upload and require session recheck.
	// Persist the returned session version/state, never the proxy URL itself.
	return out, s.Repo.SaveTikTokConnection(ctx, out)
}

func (s *Service) Claim(ctx context.Context, q *w.ClaimRequest) (*w.Lease, error) {
	ok, err := s.Repo.TikTokPublicationAllowed(ctx, q.PublicationID)
	if err != nil {
		return nil, err
	}
	if !ok || q.Kind != w.KindPublish {
		return nil, domain.ErrInvalid
	}
	out, err := s.Workflow.Claim(ctx, q)
	if err != nil || !out.Granted {
		return out, err
	}
	if out.Publication == nil || out.Publication.Provider != "tiktok" {
		return nil, domain.ErrInvalid
	}
	p := out.Publication
	if len(p.MediaSHA256) != 64 || p.MediaSizeBytes <= 0 {
		_, _ = s.Workflow.Fail(ctx, &w.FailRequest{LeaseRequest: w.LeaseRequest{AttemptID: out.AttemptID, LeaseToken: out.LeaseToken, ExpectedVersion: out.ExpectedVersion}, State: "blocked", Code: "media_verification_unavailable", Message: "Rendered file has no integrity metadata; create a new rendered revision"})
		return nil, fmt.Errorf("%w: rendered integrity metadata unavailable", domain.ErrConflict)
	}
	p.MediaURL, err = s.Gpu.PresignGet(ctx, p.Media.Bucket, p.Media.Object)
	if err != nil {
		_, _ = s.Workflow.Fail(ctx, &w.FailRequest{LeaseRequest: w.LeaseRequest{AttemptID: out.AttemptID, LeaseToken: out.LeaseToken, ExpectedVersion: out.ExpectedVersion}, State: "retry_wait", Code: "media_unavailable", Message: "Rendered media is unavailable", Retryable: true, RetryAfterSeconds: 60})
		return nil, err
	}
	return out, nil
}
func (s *Service) Allowed(ctx context.Context, attempt string) error {
	ok, err := s.Repo.TikTokAttemptAllowed(ctx, attempt)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrInvalid
	}
	return nil
}

func (s *Service) RetryAfterAbsence(ctx context.Context, pid int64, version int, confirmed bool) error {
	if !confirmed {
		return domain.ErrInvalid
	}
	return s.Repo.RetryTikTokAfterAbsence(ctx, pid, version, time.Now().UTC())
}

func (s *Service) Reconcile(ctx context.Context, pid int64, version int, remoteID, remoteURL string, operatorConfirmed bool) error {
	p, err := s.Repo.GetPublicationRecord(ctx, pid)
	if err != nil {
		return err
	}
	if p.Platform != "tiktok" || p.Version != version {
		return domain.ErrConflict
	}
	a, err := s.Repo.GetAccount(ctx, p.AccountID)
	if err != nil {
		return err
	}
	if a.RemoteAccountID == nil {
		return domain.ErrConflict
	}
	if remoteID == "" && p.UploadID != nil {
		remoteID = *p.UploadID
	}
	if remoteURL != "" {
		u, e := url.Parse(remoteURL)
		if e != nil || u.Scheme != "https" || (u.Hostname() != "www.tiktok.com" && u.Hostname() != "tiktok.com") {
			return domain.ErrInvalid
		}
		bits := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(bits) != 3 || bits[1] != "video" || strings.Trim(bits[2], "0123456789") != "" {
			return domain.ErrInvalid
		}
		if remoteID != "" && remoteID != bits[2] {
			return domain.ErrInvalid
		}
		remoteID = bits[2]
	}
	body, _ := json.Marshal(map[string]any{"publication_id": pid, "account_id": p.AccountID, "remote_account_id": *a.RemoteAccountID, "remote_id": remoteID, "operator_confirmed": operatorConfirmed && remoteURL != ""})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(s.URL, "/")+"/v1/publications/verify", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Content-Type", "application/json")
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("TikTok verification unavailable")
	}
	defer resp.Body.Close()
	var proof struct {
		Verified        bool   `json:"verified"`
		RemoteID        string `json:"remote_id"`
		RemoteURL       string `json:"remote_url"`
		RemoteAccountID string `json:"remote_account_id"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&proof) != nil || !proof.Verified || proof.RemoteID == "" || proof.RemoteAccountID != *a.RemoteAccountID {
		return fmt.Errorf("%w: TikTok has not verified a unique matching post; upload remains uncertain", domain.ErrConflict)
	}
	return s.Repo.ReconcileTikTok(ctx, pid, version, proof.RemoteID, proof.RemoteURL, operatorConfirmed && remoteURL != "", time.Now().UTC())
}
