package http

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// OperatorAuth protects the admin API; the collector has a deliberately narrower capability.
type OperatorAuth struct{ Username, Password, ServiceToken, AllowedOrigin string }

func secretEqual(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func collectorRoute(r *http.Request) bool {
	p := r.URL.Path
	if p == "/api/twitch/collector/admissions" || p == "/api/twitch/reset/collector-ack" {
		return r.Method == http.MethodPost
	}
	if p == "/api/twitch/collector-config" {
		return r.Method == http.MethodGet
	}
	if p == "/api/twitch/observations" {
		return r.Method == http.MethodPost
	}
	if p == "/api/twitch/settings" {
		return r.Method == http.MethodGet
	}
	if strings.HasPrefix(p, "/api/twitch/flows/") && strings.HasSuffix(p, "/capacity") {
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/api/twitch/flows/"), "/capacity")
		return r.Method == http.MethodGet && id != "" && !strings.Contains(id, "/")
	}
	if p == "/api/imports" {
		return r.Method == http.MethodGet || r.Method == http.MethodPost
	}
	if strings.HasPrefix(p, "/api/imports/") {
		return r.Method == http.MethodGet || (r.Method == http.MethodPost && (strings.HasSuffix(p, "/complete") || strings.HasSuffix(p, "/abort")))
	}
	return false
}
func (a *OperatorAuth) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/oauth/callback" && r.Method == http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		if a == nil {
			next.ServeHTTP(w, r)
			return
		} // Router unit tests opt in explicitly; the executable always configures auth.
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer ") && a.ServiceToken != "" && secretEqual(strings.TrimPrefix(h, "Bearer "), a.ServiceToken) {
			if !collectorRoute(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"detail": "service token cannot access this endpoint"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || a.Username == "" || a.Password == "" || !secretEqual(user, a.Username) || !secretEqual(pass, a.Password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Terex", charset="UTF-8"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "operator authentication required"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" && a.AllowedOrigin != "" && origin != a.AllowedOrigin {
				writeJSON(w, http.StatusForbidden, map[string]string{"detail": "cross-origin changes are forbidden"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
