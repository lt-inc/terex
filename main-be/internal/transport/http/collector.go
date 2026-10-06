package http

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type CollectorProxy struct {
	URL, Token string
	Client     *http.Client
}

var collectorChannelPath = regexp.MustCompile(`^channels/[A-Za-z0-9_-]{1,100}$`)
var collectorFlowCheckPath = regexp.MustCompile(`^flows/[A-Za-z0-9_-]{1,100}/check$`)

func (p *CollectorProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p == nil || p.URL == "" || p.Token == "" {
		writeJSON(w, 503, map[string]string{"detail": "collector is not configured"})
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/api/twitch/")
	if suffix != "channels" && suffix != "clips" && suffix != "check" && suffix != "status" && suffix != "categories" && !collectorChannelPath.MatchString(suffix) && !collectorFlowCheckPath.MatchString(suffix) {
		http.NotFound(w, r)
		return
	}
	base, err := url.Parse(strings.TrimRight(p.URL, "/") + "/api/" + suffix)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil {
		writeJSON(w, 503, map[string]string{"detail": "invalid collector configuration"})
		return
	}
	base.RawQuery = r.URL.RawQuery
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		writeJSON(w, 400, map[string]string{"detail": "collector request is too large"})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base.String(), bytes.NewReader(body))
	if err != nil {
		writeJSON(w, 502, map[string]string{"detail": "collector request failed"})
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Content-Type", "application/json")
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]string{"detail": "collector is unavailable"})
		return
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		writeJSON(w, 502, map[string]string{"detail": "collector response is unavailable or too large"})
		return
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		writeJSON(w, 502, map[string]string{"detail": "collector redirects are unsupported"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(data)
}
