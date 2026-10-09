package http

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"terex/proto/twitch"
)

func (a *API) revalidateTwitchImport(w http.ResponseWriter, r *http.Request) {
	var q twitch.RevalidateImportRequest
	if !decode(w, r, &q) {
		return
	}
	out, err := a.Twitch.RevalidateImport(r.Context(), chi.URLParam(r, "id"), q)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
