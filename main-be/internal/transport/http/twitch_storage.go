package http

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (a *API) twitchStorage(w http.ResponseWriter, r *http.Request) {
	out, err := a.Storage.Stats(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (a *API) previewTwitchCleanup(w http.ResponseWriter, r *http.Request) {
	out, err := a.Storage.Preview(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (a *API) cleanupTwitchStorage(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Execute bool `json:"execute"`
	}
	if !decode(w, r, &q) {
		return
	}
	out, err := a.Storage.Cleanup(r.Context(), q.Execute)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (a *API) keepTwitchImport(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Keep *bool `json:"keep"`
	}
	if !decode(w, r, &q) {
		return
	}
	if q.Keep == nil {
		badRequest(w, "keep is required")
		return
	}
	if err := a.Storage.KeepImport(r.Context(), chi.URLParam(r, "id"), *q.Keep); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"keep": *q.Keep})
}
func (a *API) keepTwitchClip(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Keep *bool `json:"keep"`
	}
	if !decode(w, r, &q) {
		return
	}
	if q.Keep == nil {
		badRequest(w, "keep is required")
		return
	}
	if err := a.Storage.KeepClip(r.Context(), chi.URLParam(r, "clipID"), *q.Keep); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"keep": *q.Keep})
}
