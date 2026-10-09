package helix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"terex/twitch-collector/internal/domain"
)

var loginPattern = regexp.MustCompile(`^[a-z0-9_]{1,25}$`)
var ClipIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,180}$`)

// LoginPattern is the Twitch login format Terex requires in broadcaster_login.
var LoginPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,25}$`)

type Client struct {
	HTTP                                                   *http.Client
	API, OAuth, ClientID, ClientSecret                     string
	mu                                                     sync.Mutex
	token                                                  string
	expires                                                time.Time
	discoveries                                            map[string]domain.DiscoveryReport
	limitMu                                                sync.Mutex
	nextRequest, rateReset                                 time.Time
	rateRemaining                                          int
	rateKnown                                              bool
	DiscoveryMaxRequests, DiscoveryRPS, DiscoveryRateFloor int
	channelCache                                           map[string]channelMetadata
}

func New(id, secret string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}, API: "https://api.twitch.tv/helix", OAuth: "https://id.twitch.tv/oauth2/token", ClientID: id, ClientSecret: secret, DiscoveryMaxRequests: discoveryEnv("TWITCH_DISCOVERY_MAX_REQUESTS", 120, 1), DiscoveryRPS: discoveryEnv("TWITCH_DISCOVERY_RPS", 5, 1), DiscoveryRateFloor: discoveryEnv("TWITCH_DISCOVERY_RATE_REMAINING_FLOOR", 50, 0)}
}
func (c *Client) Ready() bool { return c.ClientID != "" && c.ClientSecret != "" }
func Login(input string) (string, error) {
	input = strings.TrimSpace(input)
	if strings.Contains(input, "://") {
		u, err := url.Parse(input)
		if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || (u.Host != "twitch.tv" && u.Host != "www.twitch.tv") || u.RawQuery != "" || u.Fragment != "" {
			return "", errors.New("enter a Twitch login or channel URL")
		}
		input = strings.Trim(u.Path, "/")
		if strings.Contains(input, "/") {
			return "", errors.New("enter a channel URL, not a video or clip URL")
		}
	}
	input = strings.ToLower(input)
	if !loginPattern.MatchString(input) {
		return "", errors.New("invalid Twitch login")
	}
	return input, nil
}
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	if !c.Ready() {
		return "", errors.New("Twitch application credentials are not configured")
	}
	form := url.Values{"client_id": {c.ClientID}, "client_secret": {c.ClientSecret}, "grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.OAuth, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", &domain.Failure{Kind: "network", Message: "Twitch token service unavailable"}
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", &domain.Failure{Kind: "twitch_auth", Message: fmt.Sprintf("Twitch application token rejected (HTTP %d)", res.StatusCode), HTTPStatus: res.StatusCode}
	}
	var body struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&body); err != nil || body.Token == "" || body.Expires < 1 {
		return "", &domain.Failure{Kind: "twitch_response", Message: "invalid Twitch token response"}
	}
	c.token = body.Token
	c.expires = time.Now().Add(time.Duration(body.Expires)*time.Second - 30*time.Second)
	return c.token, nil
}
func (c *Client) get(ctx context.Context, path string, values url.Values, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.beforeMetadata(ctx); err != nil {
			return err
		}
		token, err := c.accessToken(ctx)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", c.API+path+"?"+values.Encode(), nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Client-Id", c.ClientID)
		res, err := c.HTTP.Do(req)
		if err != nil {
			return &domain.Failure{Kind: "network", Message: "Twitch metadata request unavailable"}
		}
		c.observeRate(res.Header)
		if res.StatusCode == http.StatusTooManyRequests {
			c.rememberRateLimit(res.Header)
			res.Body.Close()
			return c.rateFailure()
		}
		if res.StatusCode == 401 && attempt == 0 {
			res.Body.Close()
			c.mu.Lock()
			if c.token == token {
				c.token = ""
			}
			c.mu.Unlock()
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			return &domain.Failure{Kind: "twitch_metadata", Message: fmt.Sprintf("Twitch metadata request failed (HTTP %d)", res.StatusCode), HTTPStatus: res.StatusCode}
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(out)
		res.Body.Close()
		if err != nil {
			return &domain.Failure{Kind: "twitch_response", Message: "invalid Twitch metadata response"}
		}
		return nil
	}
	return &domain.Failure{Kind: "twitch_auth", Message: "Twitch application token expired"}
}
func (c *Client) ResolveChannel(ctx context.Context, input string) (domain.Channel, error) {
	login, err := Login(input)
	if err != nil {
		return domain.Channel{}, err
	}
	var body struct {
		Data []struct {
			ID, Login   string
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err = c.get(ctx, "/users", url.Values{"login": {login}}, &body); err != nil {
		return domain.Channel{}, err
	}
	if len(body.Data) != 1 {
		return domain.Channel{}, domain.ErrNotFound
	}
	u := body.Data[0]
	return domain.Channel{BroadcasterID: u.ID, Login: u.Login, DisplayName: u.DisplayName}, nil
}
func (c *Client) GetClips(ctx context.Context, ch domain.Channel, start, end time.Time, first int) ([]domain.Clip, error) {
	var body struct {
		Data []struct {
			ID              string    `json:"id"`
			BroadcasterID   string    `json:"broadcaster_id"`
			BroadcasterName string    `json:"broadcaster_name"`
			Title           string    `json:"title"`
			Views           int       `json:"view_count"`
			Created         time.Time `json:"created_at"`
			Duration        float64   `json:"duration"`
			VideoID         string    `json:"video_id"`
			Offset          *float64  `json:"vod_offset"`
		} `json:"data"`
	}
	if first < 1 || first > 100 {
		first = 100
	}
	q := url.Values{"broadcaster_id": {ch.BroadcasterID}, "first": {fmt.Sprint(first)}, "started_at": {start.UTC().Format(time.RFC3339)}, "ended_at": {end.UTC().Format(time.RFC3339)}}
	if err := c.get(ctx, "/clips", q, &body); err != nil {
		return nil, err
	}
	out := make([]domain.Clip, 0, len(body.Data))
	for _, v := range body.Data {
		if !ClipIDPattern.MatchString(v.ID) || v.BroadcasterID != ch.BroadcasterID {
			continue
		}
		out = append(out, domain.Clip{ID: v.ID, ChannelID: ch.ID, BroadcasterID: v.BroadcasterID, BroadcasterName: ch.Login, Title: v.Title, ViewCount: v.Views, CreatedAt: v.Created, ObservedAt: time.Now().UTC(), Duration: v.Duration, VideoID: v.VideoID, VODOffset: v.Offset, URL: "https://clips.twitch.tv/" + v.ID, State: "discovered"})
	}
	return out, nil
}
