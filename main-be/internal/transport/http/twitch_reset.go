package http

import (
	"net/http"
	"terex/proto/twitch"
)

func (a *API) previewTwitchReset(w http.ResponseWriter, r *http.Request) {
	out, err := a.Reset.Preview(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (a *API) startTwitchReset(w http.ResponseWriter, r *http.Request) {
	var q twitch.ResetRequest
	if !decode(w, r, &q) {
		return
	}
	out, err := a.Reset.Start(r.Context(), q)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (a *API) twitchResetStatus(w http.ResponseWriter, r *http.Request) {
	out, err := a.Reset.Status(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (a *API) acknowledgeTwitchReset(w http.ResponseWriter, r *http.Request) {
	var q twitch.ResetCollectorAck
	if !decode(w, r, &q) {
		return
	}
	if err := a.Reset.AcknowledgeCollector(r.Context(), q); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"acknowledged": true})
}
