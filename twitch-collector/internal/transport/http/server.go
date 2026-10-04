package transport

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"terex/twitch-collector/internal/domain"
)

type Service interface {
	Channels(context.Context) ([]domain.Channel, error)
	AddChannel(context.Context, string) (domain.Channel, error)
	SetChannel(context.Context, string, *bool, bool) (domain.Channel, error)
	Clips(context.Context, int, string) ([]domain.Clip, error)
	Status(context.Context) (domain.Status, error)
	Check() error
}

func Handler(service Service, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]bool{"ok": true}) })
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			provided := strings.TrimPrefix(header, "Bearer ")
			if token == "" || !strings.HasPrefix(header, "Bearer ") || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
				reply(w, 401, map[string]string{"error": "unauthorized"})
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/channels", auth(func(w http.ResponseWriter, r *http.Request) {
		out, err := service.Channels(r.Context())
		result(w, 200, out, err)
	}))
	mux.HandleFunc("POST /api/channels", auth(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		if !decode(w, r, &body) {
			return
		}
		if strings.TrimSpace(body.Input) == "" {
			reply(w, 400, map[string]string{"error": "input is required"})
			return
		}
		out, err := service.AddChannel(r.Context(), body.Input)
		result(w, 201, out, err)
	}))
	mux.HandleFunc("PATCH /api/channels/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Paused *bool `json:"paused"`
		}
		if !decode(w, r, &body) {
			return
		}
		if body.Paused == nil {
			reply(w, 400, map[string]string{"error": "paused is required"})
			return
		}
		out, err := service.SetChannel(r.Context(), r.PathValue("id"), body.Paused, false)
		result(w, 200, out, err)
	}))
	mux.HandleFunc("DELETE /api/channels/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		out, err := service.SetChannel(r.Context(), r.PathValue("id"), nil, true)
		result(w, 200, out, err)
	}))
	mux.HandleFunc("GET /api/clips", auth(func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		out, err := service.Clips(r.Context(), limit, r.URL.Query().Get("state"))
		result(w, 200, out, err)
	}))
	mux.HandleFunc("GET /api/status", auth(func(w http.ResponseWriter, r *http.Request) {
		out, err := service.Status(r.Context())
		result(w, 200, out, err)
	}))
	mux.HandleFunc("POST /api/check", auth(func(w http.ResponseWriter, r *http.Request) {
		result(w, 202, map[string]bool{"accepted": true}, service.Check())
	}))
	return mux
}
func decode(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		reply(w, 400, map[string]string{"error": "invalid request body"})
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		reply(w, 400, map[string]string{"error": "one JSON object is required"})
		return false
	}
	return true
}
func result(w http.ResponseWriter, status int, out any, err error) {
	if err == nil {
		reply(w, status, out)
		return
	}
	status = 502
	message := "collector request failed"
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status = 404
		message = "channel not found"
	case errors.Is(err, domain.ErrConflict):
		status = 409
		message = "check already running or channel limit reached"
	case errors.Is(err, domain.ErrPaused):
		status = 409
		message = "collector paused"
	case strings.Contains(err.Error(), "credentials are not configured"):
		status = 503
		message = "Twitch application credentials are not configured"
	case strings.Contains(err.Error(), "invalid Twitch login") || strings.Contains(err.Error(), "enter a"):
		status = 400
		message = err.Error()
	}
	reply(w, status, map[string]string{"error": message})
}
func reply(w http.ResponseWriter, status int, out any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(out)
}
