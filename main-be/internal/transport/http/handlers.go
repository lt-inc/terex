package http

import (
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"terex/main-be/internal/domain"
)

// ── jobs ─────────────────────────────────────────────────────────────────────

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	out, err := a.Jobs.List(r.Context(), r.URL.Query().Get("show_archived") == "true")
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) createJob(w http.ResponseWriter, r *http.Request) {
	var card domain.VideoCard
	if !decode(w, r, &card) {
		return
	}
	if strings.TrimSpace(card.YoutubeURL) == "" {
		badRequest(w, "youtubeUrl is required")
		return
	}
	id, err := a.Jobs.Create(r.Context(), card, nil)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "queued", "job_id": id})
}

func (a *API) patchJob(w http.ResponseWriter, r *http.Request) {
	var u domain.JobUpdate
	if !decode(w, r, &u) {
		return
	}
	j, err := a.Jobs.Update(r.Context(), chi.URLParam(r, "id"), u)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, j)
}

func (a *API) deleteJob(w http.ResponseWriter, r *http.Request) {
	if err := a.Jobs.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (a *API) patchSubJob(w http.ResponseWriter, r *http.Request) {
	var u domain.SubJobUpdate
	if !decode(w, r, &u) {
		return
	}
	sj, err := a.Jobs.UpdateSubJob(r.Context(), chi.URLParam(r, "id"), u)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, sj)
}

func (a *API) deleteSubJob(w http.ResponseWriter, r *http.Request) {
	if err := a.Jobs.DeleteSubJob(r.Context(), chi.URLParam(r, "id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// ── accounts ─────────────────────────────────────────────────────────────────

func (a *API) accountNames(w http.ResponseWriter, r *http.Request) {
	out, err := a.Assets.AccountNames(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) listAccounts(w http.ResponseWriter, r *http.Request) {
	out, err := a.Assets.ListAccounts(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func optional(s string) *string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return &s
}

// createAccount accepts multipart (platform, name, url, note, auth_file). The auth file is
// forwarded to gpu-be over gRPC and not stored here.
func (a *API) createAccount(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		badRequest(w, "invalid multipart form")
		return
	}
	acc := domain.Account{
		Platform: strings.ToLower(strings.TrimSpace(r.FormValue("platform"))),
		Name:     strings.TrimSpace(r.FormValue("name")),
		URL:      optional(r.FormValue("url")),
		Note:     optional(r.FormValue("note")),
	}
	if acc.Platform == "" || acc.Name == "" {
		badRequest(w, "platform and name are required")
		return
	}
	var content []byte
	if f, _, err := r.FormFile("auth_file"); err == nil {
		defer f.Close()
		content, _ = io.ReadAll(io.LimitReader(f, 4<<20))
	}
	out, err := a.Assets.CreateAccount(r.Context(), acc, content)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) patchAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var u domain.AccountUpdate
	if !decode(w, r, &u) {
		return
	}
	out, err := a.Assets.UpdateAccount(r.Context(), id, u)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := a.Assets.DeleteAccount(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (a *API) accountPublications(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	out, err := a.Publish.AccountPublications(r.Context(), id, chi.URLParam(r, "kind"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// ── banners / backgrounds ────────────────────────────────────────────────────

func (a *API) assetNames(w http.ResponseWriter, r *http.Request) {
	out, err := a.Assets.Names(r.Context(), kindParam(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) listAssets(w http.ResponseWriter, r *http.Request) {
	out, err := a.Assets.List(r.Context(), kindParam(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) presignAsset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FileName string `json:"file_name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.FileName == "" {
		badRequest(w, "file_name is required")
		return
	}
	out, err := a.Assets.PresignUpload(r.Context(), kindParam(r), in.FileName)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) registerAsset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string  `json:"name"`
		Note   *string `json:"note"`
		Bucket string  `json:"bucket"`
		Object string  `json:"object"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || in.Bucket == "" || in.Object == "" {
		badRequest(w, "name, bucket and object are required")
		return
	}
	out, err := a.Assets.Register(r.Context(), kindParam(r), strings.TrimSpace(in.Name), in.Note, in.Bucket, in.Object)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// importBackground downloads a YouTube video as a background (gpu-be) instead of a browser upload.
func (a *API) importBackground(w http.ResponseWriter, r *http.Request) {
	if kindParam(r) != domain.KindBackground {
		badRequest(w, "only backgrounds can be imported from YouTube")
		return
	}
	var in struct {
		Name string  `json:"name"`
		Note *string `json:"note"`
		URL  string  `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.URL) == "" {
		badRequest(w, "name and url are required")
		return
	}
	out, err := a.Assets.ImportBackground(r.Context(), strings.TrimSpace(in.Name), in.Note, strings.TrimSpace(in.URL))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) patchAsset(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var u domain.AssetUpdate
	if !decode(w, r, &u) {
		return
	}
	out, err := a.Assets.Update(r.Context(), kindParam(r), id, u)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) deleteAsset(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := a.Assets.Delete(r.Context(), kindParam(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// ── channels ─────────────────────────────────────────────────────────────────

func (a *API) listChannels(w http.ResponseWriter, r *http.Request) {
	out, err := a.Monitor.List(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) createChannel(w http.ResponseWriter, r *http.Request) {
	var c domain.Channel
	if !decode(w, r, &c) {
		return
	}
	if strings.TrimSpace(c.URL) == "" {
		badRequest(w, "url is required")
		return
	}
	if c.Name == "" {
		c.Name = c.URL
	}
	c.Enabled = true
	out, err := a.Monitor.Add(r.Context(), c)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) patchChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	var u domain.ChannelUpdate
	if !decode(w, r, &u) {
		return
	}
	out, err := a.Monitor.Update(r.Context(), id, u)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(w, r)
	if !ok {
		return
	}
	if err := a.Monitor.Delete(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// ── clips ────────────────────────────────────────────────────────────────────

func (a *API) listClips(w http.ResponseWriter, r *http.Request) {
	out, err := a.Publish.ListClips(r.Context(), r.URL.Query().Get("approval"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, out)
}

func (a *API) approveClip(w http.ResponseWriter, r *http.Request) {
	if a.Workflow != nil {
		var u domain.PublicationAction
		if !decode(w, r, &u) {
			return
		}
		if err := a.Workflow.ApproveVersion(r.Context(), chi.URLParam(r, "id"), u.Version); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"status": "approved"})
		return
	}
	if err := a.Publish.Approve(r.Context(), chi.URLParam(r, "id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "approved"})
}

func (a *API) rejectClip(w http.ResponseWriter, r *http.Request) {
	if err := a.Publish.Reject(r.Context(), chi.URLParam(r, "id")); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "rejected"})
}
