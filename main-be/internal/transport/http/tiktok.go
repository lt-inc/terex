package http

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"regexp"
	"strings"
	"terex/main-be/internal/domain"
	w "terex/proto/workflow"
	"time"
)

func (a *API) tikTokConnect(rw http.ResponseWriter, r *http.Request) {
	id, ok := idParam(rw, r)
	if !ok {
		return
	}
	out, err := a.TikTok.Connect(r.Context(), id)
	if err != nil {
		fail(rw, err)
		return
	}
	out.SessionRef = ""
	writeJSON(rw, 200, out)
}
func (a *API) tikTokConnection(rw http.ResponseWriter, r *http.Request) {
	id, ok := idParam(rw, r)
	if !ok {
		return
	}
	out, err := a.TikTok.Connection(r.Context(), id, chi.URLParam(r, "action"))
	if err != nil {
		fail(rw, err)
		return
	}
	out.SessionRef = ""
	writeJSON(rw, 200, out)
}
func (a *API) tikTokConnectPayload(rw http.ResponseWriter, r *http.Request) {
	if !tikTokPairCORS(rw, r) {
		return
	}
	if r.Method == http.MethodOptions {
		rw.WriteHeader(http.StatusNoContent)
		return
	}
	if a.TikTok == nil {
		writeJSON(rw, 503, map[string]string{"detail": "TikTok uploader unavailable"})
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(rw, r.Body, 1<<20))
	if err != nil || !json.Valid(data) {
		badRequest(rw, "invalid session payload")
		return
	}
	// Authentication is exclusively the one-time token enforced by uploader.
	// Operator cookies/Basic Auth never authorize this endpoint.
	var input struct {
		PairingToken string `json:"pairing_token"`
	}
	if json.Unmarshal(data, &input) != nil || len(input.PairingToken) < 32 {
		writeJSON(rw, 401, map[string]string{"detail": "one-time pairing token required"})
		return
	}
	out, err := a.TikTok.Import(r.Context(), data)
	if err != nil {
		fail(rw, err)
		return
	}
	out.SessionRef = ""
	writeJSON(rw, 200, out)
}

func (a *API) tikTokProxy(rw http.ResponseWriter, r *http.Request) {
	id, ok := idParam(rw, r)
	if !ok {
		return
	}
	var settings domain.TikTokProxySettings
	if !decode(rw, r, &settings) {
		return
	}
	out, err := a.TikTok.SetProxy(r.Context(), id, settings)
	if err != nil {
		fail(rw, err)
		return
	}
	out.SessionRef = ""
	writeJSON(rw, 200, out)
}

var extensionOrigin = regexp.MustCompile(`^chrome-extension://[a-p]{32}$`)

func tikTokPairCORS(rw http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" && !extensionOrigin.MatchString(origin) {
		writeJSON(rw, 403, map[string]string{"detail": "browser extension origin required"})
		return false
	}
	if origin != "" {
		rw.Header().Set("Access-Control-Allow-Origin", origin)
		rw.Header().Set("Vary", "Origin")
	}
	rw.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	rw.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	rw.Header().Set("Access-Control-Max-Age", "600")
	return true
}
func (a *API) tikTokWorker(rw http.ResponseWriter, r *http.Request) {
	if a.TikTok == nil || a.TikTok.WorkerToken == "" || !secretEqual(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), a.TikTok.WorkerToken) {
		writeJSON(rw, 401, map[string]string{"detail": "TikTok worker authentication required"})
		return
	}
	action := chi.URLParam(r, "action")
	if action == "claim" {
		var q w.ClaimRequest
		if !decode(rw, r, &q) {
			return
		}
		out, err := a.TikTok.Claim(r.Context(), &q)
		if err != nil {
			fail(rw, err)
			return
		}
		if out.Publication != nil {
			out.Publication.CredentialRef = w.ObjectRef{}
			out.Publication.Media = w.ObjectRef{}
		}
		writeJSON(rw, 200, out)
		return
	}
	if action == "reconcile" {
		var q struct {
			PublicationID   int64  `json:"publication_id"`
			ExpectedVersion int    `json:"expected_version"`
			RemoteID        string `json:"remote_id"`
			RemoteURL       string `json:"remote_url"`
		}
		if !decode(rw, r, &q) {
			return
		}
		if err := a.TikTok.Reconcile(r.Context(), q.PublicationID, q.ExpectedVersion, q.RemoteID, q.RemoteURL, false); err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, 200, w.Result{Accepted: true})
		return
	}
	var data json.RawMessage
	if !decode(rw, r, &data) {
		return
	}
	var lease w.LeaseRequest
	if json.Unmarshal(data, &lease) != nil || lease.AttemptID == "" {
		badRequest(rw, "attempt identity required")
		return
	}
	if action == "release" {
		ok, err := a.TikTok.Repo.ReleaseTikTokOperation(r.Context(), lease.AttemptID, lease.LeaseToken, time.Now().UTC())
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, 200, w.Result{Accepted: ok})
		return
	}
	if err := a.TikTok.Allowed(r.Context(), lease.AttemptID); err != nil {
		fail(rw, err)
		return
	}
	switch action {
	case "renew":
		out, err := a.Workflow.Renew(r.Context(), &lease)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, 200, out)
	case "checkpoint":
		var q w.CheckpointRequest
		if json.Unmarshal(data, &q) != nil {
			badRequest(rw, "invalid checkpoint")
			return
		}
		if !validTikTokState(q.State) {
			badRequest(rw, "invalid phase")
			return
		}
		out, err := a.Workflow.Checkpoint(r.Context(), &q)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, 200, out)
	case "complete":
		var q w.CompleteRequest
		if json.Unmarshal(data, &q) != nil {
			badRequest(rw, "invalid completion")
			return
		}
		if q.State != "cancelled" && (q.State != "published" || q.RemoteID == "" || q.VerifiedAt == nil) {
			badRequest(rw, "a verified TikTok post ID is required")
			return
		}
		out, err := a.Workflow.Complete(r.Context(), &q)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, 200, out)
	case "fail":
		var q w.FailRequest
		if json.Unmarshal(data, &q) != nil {
			badRequest(rw, "invalid failure")
			return
		}
		out, err := a.Workflow.Fail(r.Context(), &q)
		if err != nil {
			fail(rw, err)
			return
		}
		writeJSON(rw, 200, out)
	default:
		badRequest(rw, "unsupported worker action")
	}
}
func validTikTokState(s string) bool {
	return s == "uploading" || s == "publishing" || s == "verifying" || s == "processing" || s == "published" || s == "needs_attention" || s == "cancelled"
}
func (a *API) tikTokReconcile(rw http.ResponseWriter, r *http.Request) {
	id, ok := idParam(rw, r)
	if !ok {
		return
	}
	var q struct {
		ExpectedVersion int    `json:"expected_version"`
		RemoteID        string `json:"remote_id"`
		RemoteURL       string `json:"remote_url"`
	}
	if !decode(rw, r, &q) {
		return
	}
	if err := a.TikTok.Reconcile(r.Context(), id, q.ExpectedVersion, q.RemoteID, q.RemoteURL, true); err != nil {
		fail(rw, err)
		return
	}
	out, err := a.TikTok.Repo.GetPublicationRecord(r.Context(), id)
	if err != nil {
		fail(rw, err)
		return
	}
	writeJSON(rw, 200, out)
}

var _ = domain.ErrInvalid

func (a *API) tikTokRetryAfterAbsence(rw http.ResponseWriter, r *http.Request) {
	id, ok := idParam(rw, r)
	if !ok {
		return
	}
	var q struct {
		ExpectedVersion int  `json:"expected_version"`
		ConfirmAbsent   bool `json:"confirm_absent"`
	}
	if !decode(rw, r, &q) {
		return
	}
	if err := a.TikTok.RetryAfterAbsence(r.Context(), id, q.ExpectedVersion, q.ConfirmAbsent); err != nil {
		fail(rw, err)
		return
	}
	out, err := a.TikTok.Repo.GetPublicationRecord(r.Context(), id)
	if err != nil {
		fail(rw, err)
		return
	}
	writeJSON(rw, 200, out)
}
