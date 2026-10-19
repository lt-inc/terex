package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"terex/main-be/internal/domain"
)

func (a *API) ytFlows(w http.ResponseWriter, r *http.Request) {
	out, err := a.YouTube.Flows(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) ytFlow(w http.ResponseWriter, r *http.Request) {
	out, err := a.YouTube.Flow(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// saveYTFlow creates a flow (POST) or updates one (PATCH/PUT). A PATCH body only has to carry the
// fields that change: it is decoded over the stored flow.
func (a *API) saveYTFlow(w http.ResponseWriter, r *http.Request) {
	create := r.Method == http.MethodPost
	var f domain.YTFlow
	if !create {
		var err error
		if f, err = a.YouTube.Flow(r.Context(), chi.URLParam(r, "id")); err != nil {
			fail(w, err)
			return
		}
	}
	if !decode(w, r, &f) {
		return
	}
	if !create {
		f.ID = chi.URLParam(r, "id")
	}
	out, err := a.YouTube.Save(r.Context(), f, create)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) deleteYTFlow(w http.ResponseWriter, r *http.Request) {
	if err := a.YouTube.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (a *API) runYTFlow(w http.ResponseWriter, r *http.Request) {
	out, err := a.YouTube.RunNow(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) addYTSource(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Kind       string `json:"kind"`
		Value      string `json:"value"`
		RegionCode string `json:"region_code"`
		Language   string `json:"language"`
	}
	if !decode(w, r, &q) {
		return
	}
	out, err := a.YouTube.AddSource(r.Context(), chi.URLParam(r, "id"), q.Kind, q.Value, domain.YTSourceParams{RegionCode: q.RegionCode, Language: q.Language})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) deleteYTSource(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.ParseInt(chi.URLParam(r, "sid"), 10, 64)
	if err != nil {
		badRequest(w, "invalid source id")
		return
	}
	if err = a.YouTube.DeleteSource(r.Context(), chi.URLParam(r, "id"), sid); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true})
}

func (a *API) ytCandidates(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := a.YouTube.Candidates(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("state"), limit)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) skipYTCandidate(w http.ResponseWriter, r *http.Request) {
	if err := a.YouTube.SkipCandidate(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "video")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"skipped": true})
}

func (a *API) ytQuota(w http.ResponseWriter, r *http.Request) {
	out, err := a.YouTube.Quota(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
