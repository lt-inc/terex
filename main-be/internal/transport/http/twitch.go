package http

import (
	"github.com/go-chi/chi/v5"
	"net/http"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
)

func (a *API) twitchSettings(w http.ResponseWriter, r *http.Request) {
	out, err := a.Twitch.Settings(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) saveTwitchSettings(w http.ResponseWriter, r *http.Request) {
	var v twitch.Settings
	if !decode(w, r, &v) {
		return
	}
	out, err := a.Twitch.SaveSettings(r.Context(), v)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) reserveImport(w http.ResponseWriter, r *http.Request) {
	var v twitch.ImportRequest
	if !decode(w, r, &v) {
		return
	}
	out, err := a.Twitch.Reserve(r.Context(), v)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 202, out)
}
func (a *API) completeImport(w http.ResponseWriter, r *http.Request) {
	var v twitch.ImportCompleteRequest
	if !decode(w, r, &v) {
		return
	}
	out, err := a.Twitch.Complete(r.Context(), chi.URLParam(r, "id"), v)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 202, out)
}
func (a *API) listImports(w http.ResponseWriter, r *http.Request) {
	out, err := a.Twitch.Imports(r.Context(), r.URL.Query().Get("pending") == "true", r.URL.Query().Get("state"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) getImport(w http.ResponseWriter, r *http.Request) {
	out, err := a.Twitch.Import(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) patchImport(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Rejected *bool `json:"rejected"`
	}
	if !decode(w, r, &v) {
		return
	}
	if v.Rejected == nil {
		badRequest(w, "rejected is required")
		return
	}
	out, err := a.Twitch.Reject(r.Context(), chi.URLParam(r, "id"), *v.Rejected)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) compilations(w http.ResponseWriter, r *http.Request) {
	out, err := a.Twitch.Compilations(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) createCompilation(w http.ResponseWriter, r *http.Request) {
	var v twitch.CompilationRequest
	if !decode(w, r, &v) {
		return
	}
	out, err := a.Twitch.CreateCompilation(r.Context(), v)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 202, out)
}
func (a *API) getCompilation(w http.ResponseWriter, r *http.Request) {
	out, err := a.Twitch.Compilation(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) updateCompilation(w http.ResponseWriter, r *http.Request) {
	var v domain.CompilationUpdate
	if !decode(w, r, &v) {
		return
	}
	out, err := a.Twitch.UpdateCompilation(r.Context(), chi.URLParam(r, "id"), v)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 202, out)
}
func (a *API) autoCompilation(w http.ResponseWriter, r *http.Request) {
	out, err := a.Twitch.AutoCompilation(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}
