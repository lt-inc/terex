package http

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"terex/proto/twitch"
)

func (a *API) twitchFlowCapacity(w http.ResponseWriter, r *http.Request) {
	out, e := a.Twitch.Capacity(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) twitchPolicyPreview(w http.ResponseWriter, r *http.Request) {
	out, e := a.Twitch.PolicyPreview(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) twitchPolicyApply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !req.Confirm {
		badRequest(w, "Подтвердите переход очереди после просмотра предварительного расчёта")
		return
	}
	out, e := a.Twitch.ApplyPolicy(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) restoreTwitchFlow(w http.ResponseWriter, r *http.Request) {
	out, e := a.Twitch.RestoreFlow(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) abortTwitchImport(w http.ResponseWriter, r *http.Request) {
	var req twitch.AbortImportRequest
	if !decode(w, r, &req) {
		return
	}
	if e := a.Twitch.AbortImportFenced(r.Context(), chi.URLParam(r, "id"), req); e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"aborted": true})
}
