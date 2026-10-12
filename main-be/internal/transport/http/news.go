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

// NewsProxy forwards the panel's /api/news/* calls to the internal news-collector with its
// own bearer token. Only the collector's known routes are reachable.
type NewsProxy struct {
	URL, Token string
	Client     *http.Client
}

var newsPath = regexp.MustCompile(`^(status|articles|sources(/test|/\d{1,18}(/fetch)?)?|channels(/\d{1,18}(/(verify|test))?)?|flows(/\d{1,18}(/(generate|stories))?)?|posts(/\d{1,18}(/(approve|reject|preview|images))?)?)$`)

func (p *NewsProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p == nil || p.URL == "" || p.Token == "" {
		writeJSON(w, 503, map[string]string{"detail": "news collector is not configured"})
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/api/news/")
	if !newsPath.MatchString(suffix) {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
	default:
		writeJSON(w, 405, map[string]string{"detail": "method not allowed"})
		return
	}
	base, err := url.Parse(strings.TrimRight(p.URL, "/") + "/api/" + suffix)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil {
		writeJSON(w, 503, map[string]string{"detail": "invalid news collector configuration"})
		return
	}
	base.RawQuery = r.URL.RawQuery
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		writeJSON(w, 400, map[string]string{"detail": "request is too large"})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base.String(), bytes.NewReader(body))
	if err != nil {
		writeJSON(w, 502, map[string]string{"detail": "news collector request failed"})
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Content-Type", "application/json")
	client := p.Client
	if client == nil {
		// generation can take minutes (reasoning models), so the deadline is generous
		client = &http.Client{Timeout: 6 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]string{"detail": "news collector is unavailable"})
		return
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, (8<<20)+1))
	if err != nil || len(data) > 8<<20 {
		writeJSON(w, 502, map[string]string{"detail": "news collector response is unavailable or too large"})
		return
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		writeJSON(w, 502, map[string]string{"detail": "news collector redirects are unsupported"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	_, _ = w.Write(data)
}
