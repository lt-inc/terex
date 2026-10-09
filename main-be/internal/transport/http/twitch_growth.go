package http

import (
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (a *API) twitchObservationCandidates(w http.ResponseWriter, r *http.Request) {
	out, err := a.Twitch.ObservationCandidates(r.Context(), chi.URLParam(r, "id"), 200)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
