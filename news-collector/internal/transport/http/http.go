// Package http is the internal REST API consumed by main-be (which proxies the panel's
// /api/news/* calls here). Every /api route requires the bearer service token.
package http

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/service"
)

type API struct {
	Svc     *service.Service
	Token   string
	Log     *slog.Logger
	Started time.Time
}

func Handler(a *API) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		if !a.Svc.Healthy(a.Started) {
			http.Error(w, "stale", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	// Scraped by Prometheus inside the cluster; carries counters only and is not proxied by main-be.
	mux.Handle("GET /metrics", promhttp.Handler())
	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", a.status)

	api.HandleFunc("GET /api/sources", a.listSources)
	api.HandleFunc("POST /api/sources", a.saveSource)
	api.HandleFunc("POST /api/sources/test", a.testSource)
	api.HandleFunc("PATCH /api/sources/{id}", a.saveSource)
	api.HandleFunc("DELETE /api/sources/{id}", a.deleteSource)
	api.HandleFunc("POST /api/sources/{id}/fetch", a.fetchSource)

	api.HandleFunc("GET /api/channels", a.listChannels)
	api.HandleFunc("POST /api/channels", a.saveChannel)
	api.HandleFunc("PATCH /api/channels/{id}", a.saveChannel)
	api.HandleFunc("DELETE /api/channels/{id}", a.deleteChannel)
	api.HandleFunc("POST /api/channels/{id}/verify", a.verifyChannel)
	api.HandleFunc("POST /api/channels/{id}/test", a.testChannel)

	api.HandleFunc("GET /api/flows", a.listFlows)
	api.HandleFunc("POST /api/flows", a.saveFlow)
	api.HandleFunc("GET /api/flows/{id}", a.getFlow)
	api.HandleFunc("PATCH /api/flows/{id}", a.saveFlow)
	api.HandleFunc("PUT /api/flows/{id}", a.saveFlow)
	api.HandleFunc("DELETE /api/flows/{id}", a.deleteFlow)
	api.HandleFunc("POST /api/flows/{id}/generate", a.generate)
	api.HandleFunc("GET /api/flows/{id}/stories", a.stories)

	api.HandleFunc("GET /api/articles", a.articles)
	api.HandleFunc("GET /api/posts", a.listPosts)
	api.HandleFunc("GET /api/posts/{id}", a.getPost)
	api.HandleFunc("PATCH /api/posts/{id}", a.editPost)
	api.HandleFunc("POST /api/posts/{id}/approve", a.approvePost)
	api.HandleFunc("POST /api/posts/{id}/reject", a.rejectPost)
	api.HandleFunc("GET /api/posts/{id}/preview", a.previewPost)

	mux.Handle("/api/", a.auth(api))
	return mux
}

func (a *API) auth(next http.Handler) http.Handler {
	want := []byte(a.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		got, hasPrefix := strings.CutPrefix(header, "Bearer ")
		if len(want) == 0 || !hasPrefix || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			write(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		write(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case errors.Is(err, domain.ErrInvalid):
		write(w, http.StatusBadRequest, map[string]string{"error": trimKind(err, domain.ErrInvalid)})
	case errors.Is(err, domain.ErrConflict):
		write(w, http.StatusConflict, map[string]string{"error": trimKind(err, domain.ErrConflict)})
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		write(w, http.StatusGatewayTimeout, map[string]string{"error": "the operation timed out"})
	default:
		a.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
		write(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func trimKind(err, kind error) string {
	return strings.TrimPrefix(err.Error(), kind.Error()+": ")
}

func decode(r *http.Request, v any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		return errors.New("request body is too large")
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err = json.Unmarshal(body, v); err != nil {
		return errors.New("request body is not valid JSON")
	}
	return nil
}

func (a *API) badBody(w http.ResponseWriter, err error) {
	write(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

func id(r *http.Request) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return n, err == nil && n > 0
}

func limit(r *http.Request, def, maxLimit int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n < 1 {
		return def
	}
	return min(n, maxLimit)
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	st, err := a.Svc.Status(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, st)
}

// ---- sources ----

func (a *API) listSources(w http.ResponseWriter, r *http.Request) {
	out, err := a.Svc.ListSources(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) saveSource(w http.ResponseWriter, r *http.Request) {
	x := domain.Source{Enabled: true}
	var out domain.Source
	var err error
	if r.Method == http.MethodPost {
		if err = decode(r, &x); err != nil {
			a.badBody(w, err)
			return
		}
		x.ID = 0
		out, err = a.Svc.CreateSource(r.Context(), x)
	} else {
		n, ok := id(r)
		if !ok {
			write(w, 404, map[string]string{"error": "not found"})
			return
		}
		if x, err = a.Svc.GetSource(r.Context(), n); err != nil {
			a.fail(w, r, err)
			return
		}
		if err = decode(r, &x); err != nil {
			a.badBody(w, err)
			return
		}
		x.ID = n
		out, err = a.Svc.UpdateSource(r.Context(), x)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	code := 200
	if r.Method == http.MethodPost {
		code = 201
	}
	write(w, code, out)
}

func (a *API) testSource(w http.ResponseWriter, r *http.Request) {
	var x domain.Source
	if err := decode(r, &x); err != nil {
		a.badBody(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	out, err := a.Svc.TestFeed(ctx, x)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) deleteSource(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	if err := a.Svc.DeleteSource(r.Context(), n); err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, map[string]bool{"ok": true})
}

func (a *API) fetchSource(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	added, err := a.Svc.FetchNow(ctx, n)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, map[string]int{"new_articles": added})
}

// ---- channels ----

func (a *API) listChannels(w http.ResponseWriter, r *http.Request) {
	out, err := a.Svc.ListChannels(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) saveChannel(w http.ResponseWriter, r *http.Request) {
	var c domain.Channel
	var err error
	if r.Method == http.MethodPatch {
		n, ok := id(r)
		if !ok {
			write(w, 404, map[string]string{"error": "not found"})
			return
		}
		if c, err = a.Svc.GetChannel(r.Context(), n); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if err = decode(r, &c); err != nil {
		a.badBody(w, err)
		return
	}
	if r.Method == http.MethodPost {
		c.ID = 0
	} else {
		c.ID, _ = id(r)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	out, err := a.Svc.SaveChannel(ctx, c)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	code := 200
	if r.Method == http.MethodPost {
		code = 201
	}
	write(w, code, out)
}

func (a *API) deleteChannel(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	if err := a.Svc.DeleteChannel(r.Context(), n); err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, map[string]bool{"ok": true})
}

func (a *API) verifyChannel(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	out, err := a.Svc.VerifyChannel(ctx, n)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) testChannel(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := a.Svc.TestChannel(ctx, n); err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, map[string]bool{"ok": true})
}

// ---- flows ----

func (a *API) listFlows(w http.ResponseWriter, r *http.Request) {
	out, err := a.Svc.ListFlows(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) getFlow(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	out, err := a.Svc.GetFlow(r.Context(), n)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) saveFlow(w http.ResponseWriter, r *http.Request) {
	var f domain.Flow
	var err error
	if r.Method != http.MethodPost { // partial updates, e.g. {"enabled": true}, keep the other fields
		n, ok := id(r)
		if !ok {
			write(w, 404, map[string]string{"error": "not found"})
			return
		}
		if f, err = a.Svc.GetFlow(r.Context(), n); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	if err = decode(r, &f); err != nil {
		a.badBody(w, err)
		return
	}
	if r.Method == http.MethodPost {
		f.ID = 0
	} else {
		f.ID, _ = id(r)
	}
	out, err := a.Svc.SaveFlow(r.Context(), f)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	code := 200
	if r.Method == http.MethodPost {
		code = 201
	}
	write(w, code, out)
}

func (a *API) deleteFlow(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	if err := a.Svc.DeleteFlow(r.Context(), n); err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, map[string]bool{"ok": true})
}

func (a *API) generate(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	out, err := a.Svc.GenerateNow(ctx, n)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) stories(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	out, err := a.Svc.FlowCandidates(r.Context(), n)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) articles(w http.ResponseWriter, r *http.Request) {
	flow, _ := strconv.ParseInt(r.URL.Query().Get("flow_id"), 10, 64)
	out, err := a.Svc.RecentArticles(r.Context(), flow, limit(r, 50, 200))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

// ---- posts ----

func (a *API) listPosts(w http.ResponseWriter, r *http.Request) {
	flow, _ := strconv.ParseInt(r.URL.Query().Get("flow_id"), 10, 64)
	status := r.URL.Query().Get("status")
	if status != "" && !validStatus(status) {
		a.badBody(w, errors.New("unknown status"))
		return
	}
	out, err := a.Svc.ListPosts(r.Context(), flow, status, limit(r, 50, 200))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func validStatus(s string) bool {
	switch s {
	case domain.PostPending, domain.PostApproved, domain.PostRejected, domain.PostPublished, domain.PostFailed, domain.PostExpired:
		return true
	}
	return false
}

func (a *API) getPost(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	out, err := a.Svc.GetPost(r.Context(), n)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) editPost(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	var e service.PostEdit
	if err := decode(r, &e); err != nil {
		a.badBody(w, err)
		return
	}
	out, err := a.Svc.EditPost(r.Context(), n, e)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) approvePost(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	var body struct {
		ScheduledAt *time.Time `json:"scheduled_at"`
	}
	if err := decode(r, &body); err != nil {
		a.badBody(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := a.Svc.ApprovePost(ctx, n, body.ScheduledAt)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

func (a *API) rejectPost(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	out, err := a.Svc.RejectPost(r.Context(), n)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, out)
}

// previewPost returns the exact Telegram HTML so the panel can show what will be sent.
func (a *API) previewPost(w http.ResponseWriter, r *http.Request) {
	n, ok := id(r)
	if !ok {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	p, err := a.Svc.GetPost(r.Context(), n)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	f, err := a.Svc.GetFlow(r.Context(), p.FlowID)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	write(w, 200, map[string]string{"html": service.RenderHTML(p, f.Language)})
}
