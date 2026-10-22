package http

import (
	"net/http"
	"strings"
)

// watermarkFrames returns stills of the newest video of a channel (or of a given video) for the region
// editor, together with the regions the automatic detector suggests.
func (a *API) watermarkFrames(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.URL) == "" {
		badRequest(w, "url is required")
		return
	}
	out, err := a.Watermark.Preview(r.Context(), in.URL)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
