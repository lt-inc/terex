package http

import (
	"net/http"
	"terex/main-be/internal/domain"
)

func (a *API) twitchMetadataPreview(w http.ResponseWriter, r *http.Request) {
	var request domain.TwitchMetadataPreviewRequest
	if !decode(w, r, &request) {
		return
	}
	result, err := a.Twitch.MetadataPreview(r.Context(), request)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
