// Package http is the REST API consumed by the FE. Handlers only translate HTTP <-> services.
package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"terex/main-be/internal/domain"
	"terex/main-be/internal/service/assets"
	"terex/main-be/internal/service/jobs"
	"terex/main-be/internal/service/monitor"
	"terex/main-be/internal/service/publish"
	"terex/main-be/internal/service/storagelifecycle"
	tiktokservice "terex/main-be/internal/service/tiktok"
	twitchservice "terex/main-be/internal/service/twitch"
	"terex/main-be/internal/service/twitchreset"
	"terex/main-be/internal/service/workflow"
)

type API struct {
	Jobs      *jobs.Service
	Assets    *assets.Service
	Publish   *publish.Manager
	Monitor   *monitor.Service
	Workflow  *workflow.Service
	Twitch    *twitchservice.Service
	Storage   *storagelifecycle.Service
	Reset     *twitchreset.Service
	Auth      *OperatorAuth
	Collector *CollectorProxy
	News      *NewsProxy
	TikTok    *tiktokservice.Service
}

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })

	r.Route("/api", func(r chi.Router) {
		r.Use(a.Auth.Protect)
		if a.TikTok != nil {
			r.Post("/tiktok/connect/payload", a.tikTokConnectPayload)
			r.Options("/tiktok/connect/payload", a.tikTokConnectPayload)
			r.Post("/tiktok/worker/{action:claim|renew|checkpoint|complete|fail|reconcile|release}", a.tikTokWorker)
			r.Post("/accounts/{id}/tiktok/connect", a.tikTokConnect)
			r.Get("/accounts/{id}/tiktok/status", a.tikTokConnection)
			r.Post("/accounts/{id}/tiktok/{action:check|disconnect|browser-help}", a.tikTokConnection)
			r.Post("/publications/{id}/tiktok/reconcile", a.tikTokReconcile)
			r.Post("/publications/{id}/tiktok/retry-after-absence", a.tikTokRetryAfterAbsence)
		}
		r.Handle("/news/*", a.News)
		if a.Twitch != nil {
			r.Post("/twitch/metadata/preview", a.twitchMetadataPreview)
			r.Get("/twitch/flows", a.twitchFlows)
			r.Post("/twitch/flows", a.saveTwitchFlow)
			r.Get("/twitch/flows/{id}", a.twitchFlow)
			r.Put("/twitch/flows/{id}", a.saveTwitchFlow)
			r.Patch("/twitch/flows/{id}", a.saveTwitchFlow)
			r.Delete("/twitch/flows/{id}", a.deleteTwitchFlow)
			r.Post("/twitch/flows/{id}/restore", a.restoreTwitchFlow)
			r.Get("/twitch/flows/{id}/capacity", a.twitchFlowCapacity)
			r.Get("/twitch/flows/{id}/diagnostics", a.twitchFlowDiagnostics)
			r.Get("/twitch/flows/{id}/observation-candidates", a.twitchObservationCandidates)
			r.Post("/twitch/collector/admissions", a.twitchAdmissions)
			r.Post("/twitch/collector/pressure", a.twitchPressure)
			r.Get("/twitch/policy/preview", a.twitchPolicyPreview)
			r.Post("/twitch/policy/apply", a.twitchPolicyApply)
			r.Get("/twitch/flows/{id}/clips", a.twitchFlowClips)
			r.Post("/twitch/flows/{id}/clips/{sourceID}/{action:retry|exclude}", a.actFlowSource)
			r.Post("/twitch/flows/{id}/check", a.Collector.ServeHTTP)
			r.Get("/twitch/categories", a.Collector.ServeHTTP)
			r.Get("/twitch/collector-config", a.twitchCollectorConfig)
			r.Post("/twitch/observations", a.observeTwitchSources)
			r.Get("/twitch/sources/{id}/preview", a.twitchSourcePreview)
			r.Get("/clips/{id}/preview", a.clipPreview)
			r.Get("/twitch/settings", a.twitchSettings)
			r.Put("/twitch/settings", a.saveTwitchSettings)
			r.Get("/twitch/channels", a.Collector.ServeHTTP)
			r.Post("/twitch/channels", a.Collector.ServeHTTP)
			r.Put("/twitch/channels", a.Collector.ServeHTTP)
			r.Patch("/twitch/channels/{id}", a.Collector.ServeHTTP)
			r.Delete("/twitch/channels/{id}", a.Collector.ServeHTTP)
			r.Get("/twitch/clips", a.Collector.ServeHTTP)
			r.Get("/twitch/status", a.Collector.ServeHTTP)
			r.Post("/twitch/check", a.Collector.ServeHTTP)
			r.Get("/imports", a.listImports)
			r.Post("/imports", a.reserveImport)
			r.Get("/imports/{id}", a.getImport)
			r.Patch("/imports/{id}", a.patchImport)
			r.Post("/imports/{id}/complete", a.completeImport)
			r.Post("/imports/{id}/revalidate", a.revalidateTwitchImport)
			r.Post("/imports/{id}/abort", a.abortTwitchImport)
			r.Get("/compilations", a.compilations)
			r.Post("/compilations", a.createCompilation)
			r.Post("/compilations/auto", a.autoCompilation)
			r.Get("/compilations/{id}", a.getCompilation)
			r.Put("/compilations/{id}", a.updateCompilation)
		}
		if a.Storage != nil {
			r.Get("/twitch/storage", a.twitchStorage)
			r.Get("/twitch/storage/cleanup/preview", a.previewTwitchCleanup)
			r.Post("/twitch/storage/cleanup", a.cleanupTwitchStorage)
			r.Post("/twitch/imports/{id}/retention", a.keepTwitchImport)
			r.Post("/twitch/clips/{clipID}/retention", a.keepTwitchClip)
		}
		if a.Reset != nil {
			r.Post("/twitch/reset/preview", a.previewTwitchReset)
			r.Post("/twitch/reset", a.startTwitchReset)
			r.Get("/twitch/reset", a.twitchResetStatus)
			r.Post("/twitch/reset/collector-ack", a.acknowledgeTwitchReset)
		}
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
	recordRequestError(w, err)
	var wait *domain.PreparationWaitError
	if errors.As(err, &wait) {
		writeJSON(w, http.StatusConflict, map[string]any{"detail": wait.Message, "code": wait.Code, "next_check_at": wait.NextCheckAt})
		return
	}
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
	reason := requestErrorReason(err)
	if code == http.StatusInternalServerError && (reason == "deadline_exceeded" || reason == "dependency_unavailable") {
		code = http.StatusServiceUnavailable
		w.Header().Set("Retry-After", "5")
	}
	writeJSON(w, code, map[string]string{"detail": err.Error(), "code": reason})
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
