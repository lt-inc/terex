// Package http is the REST API consumed by the FE. Handlers only translate HTTP <-> services.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"terex/main-be/internal/domain"
	"terex/main-be/internal/service/assets"
	"terex/main-be/internal/service/jobs"
	"terex/main-be/internal/service/monitor"
	"terex/main-be/internal/service/publish"
	"terex/main-be/internal/service/workflow"
)

type API struct {
	Jobs     *jobs.Service
	Assets   *assets.Service
	Publish  *publish.Manager
	Monitor  *monitor.Service
	Workflow *workflow.Service
}

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })

	r.Route("/api", func(r chi.Router) {
		// jobs
		r.Get("/jobs", a.listJobs)
		r.Post("/jobs", a.createJob)
		r.Patch("/jobs/{id}", a.patchJob)
		r.Delete("/jobs/{id}", a.deleteJob)
		r.Patch("/sub_jobs/{id}", a.patchSubJob)
		r.Delete("/sub_jobs/{id}", a.deleteSubJob)

		// accounts
		r.Get("/accounts", a.accountNames)
		r.Get("/accounts/list", a.listAccounts)
		r.Post("/accounts/create", a.createAccount)
		r.Patch("/accounts/{id}", a.patchAccount)
		r.Delete("/accounts/{id}", a.deleteAccount)
		r.Get("/accounts/{id}/{kind:clips|scheduled|published}", a.accountPublications)

		// banners / backgrounds
		r.Route("/{kind:banners|backgrounds}", func(r chi.Router) {
			r.Get("/", a.assetNames)
			r.Get("/list", a.listAssets)
			r.Post("/presign", a.presignAsset)
			r.Post("/", a.registerAsset)
			r.Patch("/{id}", a.patchAsset)
			r.Delete("/{id}", a.deleteAsset)
		})

		// channels (monitoring)
		r.Get("/channels", a.listChannels)
		r.Post("/channels", a.createChannel)
		r.Patch("/channels/{id}", a.patchChannel)
		r.Delete("/channels/{id}", a.deleteChannel)

		// clips / approval
		r.Get("/clips", a.listClips)
		r.Post("/clips/{id}/approve", a.approveClip)
		r.Post("/clips/{id}/reject", a.rejectClip)
		if a.Workflow != nil {
			r.Get("/clips/{id}/editor", a.clipEditor)
			r.Put("/clips/{id}/editor", a.saveClipEditor)
			r.Post("/clips/{id}/render", a.retryClipRender)
			r.Post("/clips/{id}/restore", a.restoreClipRevision)
			r.Get("/sub_jobs/{id}/targets", a.subJobTargets)
			r.Put("/sub_jobs/{id}/targets", a.saveSubJobTargets)
			r.Get("/publications", a.listPublications)
			r.Patch("/publications/{id}", a.patchPublication)
			r.Post("/publications/{id}/{action:approve|retry|cancel|publish-now}", a.publicationAction)
			r.Get("/publications/{id}/history", a.publicationHistory)
			r.Get("/publications/{id}/events", a.publicationHistory)
			r.Post("/accounts/{id}/oauth/connect", a.accountConnect)
			r.Post("/accounts/{id}/oauth/check", a.accountConnectionCheck)
			r.Post("/accounts/{id}/oauth/disconnect", a.accountDisconnect)
			r.Post("/accounts/{id}/oauth/confirm", a.accountConfirm)
			r.Get("/accounts/oauth/callback", a.oauthCallback)
		}
	})
	return r
}

// ── helpers ──────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if errors.Is(err, domain.ErrNotFound) {
		code = http.StatusNotFound
	}
	if errors.Is(err, domain.ErrConflict) {
		code = http.StatusConflict
	}
	if errors.Is(err, domain.ErrInvalid) {
		code = http.StatusBadRequest
	}
	if code == 500 {
		slog.Error("request failed", "err", err)
	}
	writeJSON(w, code, map[string]string{"detail": err.Error()})
}

func badRequest(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"detail": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		badRequest(w, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func idParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		badRequest(w, "invalid id")
		return 0, false
	}
	return id, true
}

func kindParam(r *http.Request) domain.AssetKind {
	if chi.URLParam(r, "kind") == "backgrounds" {
		return domain.KindBackground
	}
	return domain.KindBanner
}
