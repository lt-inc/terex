package http

import (
	"net/http"
	"terex/proto/twitch"
)

func (a *API) twitchAdmissions(w http.ResponseWriter, r *http.Request) {
	var q twitch.AdmissionRequest
	if !decode(w, r, &q) {
		return
	}
	out, err := a.Twitch.DownloadAdmissions(r.Context(), q)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
