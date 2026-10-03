package googleauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/repository/youtube"
)

const Scope = "https://www.googleapis.com/auth/youtube.force-ssl"

type OAuthState struct {
	AccountID, Verifier, RedirectURL string
	ExpiresAt                        time.Time
}

// Service serializes token refresh and disconnect with each account's lock.
// This deployment has one GPU gateway; a second gateway requires a distributed
// credential lease instead of sharing an in-process mutex.
type Service struct {
	Repo                                domain.CredentialRepository
	HTTP                                *http.Client
	ClientID, ClientSecret, RedirectURL string
	AuthURL, TokenURL                   string
	Now                                 func() time.Time
	mu                                  sync.Mutex
	locks                               map[string]*sync.Mutex
	states                              map[string]OAuthState
	YouTube                             func(youtube.TokenSource) *youtube.Client
}

func New(repo domain.CredentialRepository, id, secret, redirect string) *Service {
	return &Service{Repo: repo, HTTP: &http.Client{Timeout: 30 * time.Second}, ClientID: id, ClientSecret: secret, RedirectURL: redirect, AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", Now: time.Now, locks: map[string]*sync.Mutex{}, states: map[string]OAuthState{}, YouTube: youtube.New}
}

func random() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), err
}
func (s *Service) lock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[id] = l
	}
	return l
}

func (s *Service) Begin(accountID string) (string, string, error) {
	state, err := random()
	if err != nil {
		return "", "", err
	}
	u, err := s.BeginWithState(accountID, state)
	return u, state, err
}

func (s *Service) BeginWithState(accountID, state string) (string, error) {
	return s.BeginWithRedirect(accountID, state, s.RedirectURL)
}

func (s *Service) BeginWithRedirect(accountID, state, redirect string) (string, error) {
	if s.ClientID == "" || s.ClientSecret == "" || redirect == "" {
		return "", fmt.Errorf("Google OAuth is not configured")
	}
	u, err := url.Parse(redirect)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid Google OAuth callback URL")
	}
	if s.RedirectURL != "" && redirect != s.RedirectURL {
		return "", fmt.Errorf("OAuth callback does not match configured redirect URL")
	}
	if len(state) < 24 || len(state) > 512 {
		return "", fmt.Errorf("OAuth state must be a random opaque value")
	}
	verifier, err := random()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(verifier))
	s.mu.Lock()
	for k, v := range s.states {
		if !v.ExpiresAt.After(s.Now()) {
			delete(s.states, k)
		}
	}
	s.states[state] = OAuthState{AccountID: accountID, Verifier: verifier, RedirectURL: redirect, ExpiresAt: s.Now().Add(10 * time.Minute)}
	s.mu.Unlock()
	q := url.Values{"client_id": {s.ClientID}, "redirect_uri": {redirect}, "scope": {Scope}, "response_type": {"code"}, "access_type": {"offline"}, "prompt": {"consent"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "code_challenge_method": {"S256"}}
	return s.AuthURL + "?" + q.Encode(), nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func (s *Service) token(ctx context.Context, q url.Values) (tokenResponse, error) {
	q.Set("client_id", s.ClientID)
	q.Set("client_secret", s.ClientSecret)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, s.TokenURL, strings.NewReader(q.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.HTTP.Do(r)
	if err != nil {
		return tokenResponse{}, err
	}
	defer res.Body.Close()
	var t tokenResponse
	if err = json.NewDecoder(io.LimitReader(res.Body, 64*1024)).Decode(&t); err != nil {
		return t, fmt.Errorf("invalid OAuth token response")
	}
	if t.Error == "invalid_grant" {
		return t, domain.ErrReauthentication
	}
	if res.StatusCode != 200 || t.Error != "" || t.AccessToken == "" || t.ExpiresIn <= 0 {
		return t, fmt.Errorf("Google OAuth token exchange failed (HTTP %d)", res.StatusCode)
	}
	return t, nil
}

func (s *Service) Exchange(ctx context.Context, state, code string) (domain.GoogleCredential, error) {
	return s.ExchangeVerified(ctx, "", state, code, "")
}

func (s *Service) ExchangeVerified(ctx context.Context, accountID, state, code, expectedChannel string) (domain.GoogleCredential, error) {
	s.mu.Lock()
	st, ok := s.states[state]
	delete(s.states, state)
	s.mu.Unlock()
	if !ok || !st.ExpiresAt.After(s.Now()) || code == "" || (accountID != "" && st.AccountID != accountID) {
		return domain.GoogleCredential{}, fmt.Errorf("OAuth state is invalid or expired; reconnect account")
	}
	l := s.lock(st.AccountID)
	l.Lock()
	defer l.Unlock()
	t, err := s.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {st.RedirectURL}, "code_verifier": {st.Verifier}})
	if err != nil {
		return domain.GoogleCredential{}, err
	}
	c := domain.GoogleCredential{AccountID: st.AccountID, AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, ExpiresAt: s.Now().Add(time.Duration(t.ExpiresIn) * time.Second), Scopes: t.Scope}
	if c.RefreshToken == "" {
		old, e := s.Repo.Get(ctx, st.AccountID)
		if e == nil {
			c.RefreshToken = old.RefreshToken
		}
	}
	if c.RefreshToken == "" {
		return c, fmt.Errorf("Google did not grant offline access; revoke old grant and reconnect")
	}
	if t.Scope != "" && !strings.Contains(" "+t.Scope+" ", " "+Scope+" ") {
		return c, fmt.Errorf("required YouTube scope was not granted")
	}
	c.ChannelID, c.ChannelTitle, err = s.YouTube(func(context.Context) (string, error) { return c.AccessToken, nil }).Channel(ctx)
	if err != nil {
		return c, err
	}
	if expectedChannel != "" && c.ChannelID != expectedChannel {
		return c, fmt.Errorf("selected YouTube channel does not match account; select the original channel")
	}
	if err = s.Repo.Put(ctx, c); err != nil {
		return c, err
	}
	return c, nil
}

func (s *Service) AccessToken(ctx context.Context, id string) (string, error) {
	l := s.lock(id)
	l.Lock()
	defer l.Unlock()
	c, err := s.Repo.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if c.ExpiresAt.After(s.Now().Add(time.Minute)) && c.AccessToken != "" {
		return c.AccessToken, nil
	}
	if c.RefreshToken == "" {
		return "", domain.ErrReauthentication
	}
	t, err := s.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}})
	if err != nil {
		return "", err
	}
	c.AccessToken = t.AccessToken
	c.ExpiresAt = s.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
	if t.RefreshToken != "" {
		c.RefreshToken = t.RefreshToken
	}
	if t.Scope != "" {
		c.Scopes = t.Scope
	}
	if err = s.Repo.Put(ctx, c); err != nil {
		return "", err
	}
	return c.AccessToken, nil
}

func (s *Service) Check(ctx context.Context, id string) (domain.GoogleCredential, error) {
	t, err := s.AccessToken(ctx, id)
	if err != nil {
		return domain.GoogleCredential{}, err
	}
	c, err := s.Repo.Get(ctx, id)
	if err != nil {
		return c, err
	}
	channel, title, err := s.YouTube(func(context.Context) (string, error) { return t, nil }).Channel(ctx)
	if err != nil {
		return c, err
	}
	if channel != c.ChannelID {
		return c, fmt.Errorf("connected YouTube channel changed")
	}
	c.ChannelTitle = title
	return c, nil
}

func (s *Service) Disconnect(ctx context.Context, id string) error {
	l := s.lock(id)
	l.Lock()
	defer l.Unlock()
	return s.Repo.Delete(ctx, id)
}

func IsReauthentication(err error) bool { return errors.Is(err, domain.ErrReauthentication) }
