package http

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"terex/main-be/internal/domain"
)

func (a *API) clipEditor(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflow.Editor(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) saveClipEditor(w http.ResponseWriter, r *http.Request) {
	var u domain.EditRequest
	if !decode(w, r, &u) {
		return
	}
	out, err := a.Workflow.SaveEditor(r.Context(), chi.URLParam(r, "id"), u)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) subJobTargets(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflow.Targets(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) saveSubJobTargets(w http.ResponseWriter, r *http.Request) {
	var in domain.TargetsRequest
	if !decode(w, r, &in) {
		return
	}
	out, err := a.Workflow.SaveTargets(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) retryClipRender(w http.ResponseWriter, r *http.Request) {
	var in domain.PublicationAction
	if !decode(w, r, &in) {
		return
	}
	out, err := a.Workflow.RetryRender(r.Context(), chi.URLParam(r, "id"), in.Version)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) restoreClipRevision(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Version    int   `json:"expected_version"`
		RevisionID int64 `json:"revision_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, err := a.Workflow.RestoreEditor(r.Context(), chi.URLParam(r, "id"), in.Version, in.RevisionID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func queryTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	return &t, err
}
func (a *API) listPublications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err := queryTime(q.Get("from"))
	if err != nil {
		badRequest(w, "from must be RFC3339 with timezone")
		return
	}
	to, err := queryTime(q.Get("to"))
	if err != nil {
		badRequest(w, "to must be RFC3339 with timezone")
		return
	}
	if from != nil && to != nil && !to.After(*from) {
		badRequest(w, "to must be after from")
		return
	}
	var aid int64
	if q.Get("account_id") != "" {
		aid, err = strconv.ParseInt(q.Get("account_id"), 10, 64)
		if err != nil || aid < 1 {
			badRequest(w, "invalid account_id")
			return
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	state := q.Get("status")
	if state == "" {
		state = q.Get("state")
	}
	out, err := a.Workflow.Publications(r.Context(), domain.PublicationFilter{From: from, To: to, AccountID: aid, Platform: q.Get("platform"), State: state, Limit: limit, Offset: offset})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) patchPublication(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var u domain.PublicationPatch
	if !decode(w, r, &u) {
		return
	}
	out, err := a.Workflow.PatchPublication(r.Context(), id, u)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) publicationAction(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var u domain.PublicationAction
	if !decode(w, r, &u) {
		return
	}
	out, err := a.Workflow.ActPublicationWithOverride(r.Context(), id, chi.URLParam(r, "action"), u.Version, u.FreshnessOverride)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) publicationHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	out, err := a.Workflow.Events(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) accountConnect(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var u domain.PublicationAction
	if !decode(w, r, &u) {
		return
	}
	url, err := a.Workflow.Connect(r.Context(), id, u.Version)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"url": url})
}
func (a *API) accountConnectionCheck(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	out, err := a.Workflow.CheckConnection(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) accountDisconnect(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var u domain.PublicationAction
	if !decode(w, r, &u) {
		return
	}
	out, err := a.Workflow.Disconnect(r.Context(), id, u.Version)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) accountConfirm(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var u struct {
		Version  int    `json:"expected_version"`
		RemoteID string `json:"remote_account_id"`
	}
	if !decode(w, r, &u) {
		return
	}
	out, err := a.Workflow.ConfirmConnection(r.Context(), id, u.Version, u.RemoteID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) oauthCallback(w http.ResponseWriter, r *http.Request) {
	dest := strings.TrimRight(a.Workflow.AppURL, "/") + "/admin/accounts"
	if providerErr := r.URL.Query().Get("error"); providerErr != "" {
		http.Redirect(w, r, dest+"?oauth_error="+url.QueryEscape(providerErr), http.StatusSeeOther)
		return
	}
	id, err := a.Workflow.OAuthCallback(r.Context(), r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if err != nil {
		http.Redirect(w, r, dest+"?oauth_error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, dest+"?oauth_connected="+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
