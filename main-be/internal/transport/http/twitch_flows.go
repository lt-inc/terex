package http

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"time"
)

func (a *API) twitchFlows(w http.ResponseWriter, r *http.Request) {
	out, e := a.Twitch.Flows(r.Context())
	if r.URL.Query().Get("include_archived") == "true" {
		out, e = a.Twitch.FlowsIncludingArchived(r.Context())
	}
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) twitchFlow(w http.ResponseWriter, r *http.Request) {
	out, e := a.Twitch.Flow(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) saveTwitchFlow(w http.ResponseWriter, r *http.Request) {
	var f twitch.Flow
	if !decode(w, r, &f) {
		return
	}
	create := r.Method == http.MethodPost
	if !create {
		f.ID = chi.URLParam(r, "id")
	}
	if e := a.resolveFlowChannels(r.Context(), &f, create); e != nil {
		fail(w, e)
		return
	}
	out, e := a.Twitch.SaveFlow(r.Context(), f, create)
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) deleteTwitchFlow(w http.ResponseWriter, r *http.Request) {
	if e := a.Twitch.DeleteFlow(r.Context(), chi.URLParam(r, "id")); e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"deleted": true, "archived": true})
}
func (a *API) twitchCollectorConfig(w http.ResponseWriter, r *http.Request) {
	out, e := a.Twitch.CollectorConfig(r.Context())
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) observeTwitchSources(w http.ResponseWriter, r *http.Request) {
	var q twitch.ObservationRequest
	if !decode(w, r, &q) {
		return
	}
	out, e := a.Twitch.Observations(r.Context(), q)
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"imports": out})
}
func (a *API) twitchFlowClips(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	ids, e := a.collectorFilterSources(r, chi.URLParam(r, "id"), q.Get("state"))
	if e != nil {
		fail(w, e)
		return
	}
	out, e := a.Twitch.FlowClips(r.Context(), chi.URLParam(r, "id"), domain.FlowClipQuery{Limit: limit, Offset: offset, State: q.Get("state"), Format: q.Get("format"), BroadcasterID: q.Get("broadcaster_id"), Q: q.Get("q"), CollectorSourceIDs: ids, ShowFiltered: q.Get("show_filtered") == "true"})
	if e != nil {
		fail(w, e)
		return
	}
	a.enrichFlowClips(r, &out)
	writeJSON(w, 200, out)
}
func (a *API) actFlowSource(w http.ResponseWriter, r *http.Request) {
	if chi.URLParam(r, "action") == "retry" {
		source, e := a.Twitch.FlowImport(r.Context(), chi.URLParam(r, "sourceID"), chi.URLParam(r, "id"))
		if e != nil {
			fail(w, e)
			return
		}
		if source.Excluded || source.Rejected {
			fail(w, domain.ErrConflict)
			return
		}
		if source.MediaState != "ready" {
			var status struct {
				Items []struct {
					ClipID string `json:"clip_id"`
					State  string `json:"state"`
				} `json:"items"`
			}
			if e = a.Collector.call(r.Context(), http.MethodGet, "flows/"+url.PathEscape(source.FlowID)+"/clip-status?source_ids="+url.QueryEscape(source.Source.ClipID), nil, &status); e != nil {
				fail(w, e)
				return
			}
			failed := false
			for _, item := range status.Items {
				failed = failed || (item.ClipID == source.Source.ClipID && item.State == "failed")
			}
			if !failed {
				fail(w, domain.ErrConflict)
				return
			}
			// The collector owns the failed task identity. Its revalidation call
			// fences the coordinator before the local task can be reactivated.
			if e = a.Collector.call(r.Context(), http.MethodPost, "clips/"+url.PathEscape(source.Source.ClipID)+"/retry", nil, nil); e != nil {
				fail(w, e)
				return
			}
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
	}
	excluded := true
	if chi.URLParam(r, "action") == "exclude" {
		var q struct {
			Excluded *bool `json:"excluded"`
		}
		if r.ContentLength != 0 && !decode(w, r, &q) {
			return
		}
		if q.Excluded != nil {
			excluded = *q.Excluded
		}
	}
	if e := a.Twitch.ActSource(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "sourceID"), chi.URLParam(r, "action"), excluded); e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) twitchSourcePreview(w http.ResponseWriter, r *http.Request) {
	out, e := a.Twitch.SourcePreview(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("flow_id"))
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}
func (a *API) clipPreview(w http.ResponseWriter, r *http.Request) {
	rid, _ := strconv.ParseInt(r.URL.Query().Get("revision_id"), 10, 64)
	out, e := a.Twitch.RevisionPreview(r.Context(), chi.URLParam(r, "id"), rid, r.URL.Query().Get("kind"))
	if e != nil {
		fail(w, e)
		return
	}
	writeJSON(w, 200, out)
}

// Enrichment is bounded to this page and carries no video URLs. Main media and
// output status remain authoritative after download verification.
func (a *API) enrichFlowClips(r *http.Request, page *domain.FlowClipPage) {
	p := a.Collector
	if p == nil || p.URL == "" || p.Token == "" || len(page.Items) == 0 {
		return
	}
	ids := []string{}
	for _, v := range page.Items {
		ids = append(ids, v.Source.ClipID)
	}
	endpoint := strings.TrimRight(p.URL, "/") + "/api/flows/" + url.PathEscape(chi.URLParam(r, "id")) + "/clip-status?source_ids=" + url.QueryEscape(strings.Join(ids, ","))
	req, e := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if e != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, e := client.Do(req)
	if e != nil {
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return
	}
	var data struct {
		Items []struct {
			ClipID        string     `json:"clip_id"`
			State         string     `json:"state"`
			Reason        string     `json:"reason"`
			NextAttemptAt *time.Time `json:"next_attempt_at"`
			StartedAt     *time.Time `json:"started_at"`
			Attempt       int        `json:"attempt"`
		} `json:"items"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&data) != nil {
		return
	}
	for n := range page.Items {
		row := &page.Items[n]
		if row.MediaID != "" || row.Excluded || row.Rejected || row.SourceState == "filtered" {
			continue
		}
		for _, v := range data.Items {
			if v.ClipID != row.Source.ClipID {
				continue
			}
			switch v.State {
			case "queued", "waiting", "reserved":
				row.SourceState = "queued"
			case "waiting_assignment", "authorized", "verification_pending":
				row.SourceState = v.State
			case "downloading", "uploading":
				row.SourceState = "downloading"
			case "importing", "verifying", "waiting_quota", "waiting_capacity", "retry_wait":
				row.SourceState = v.State
			case "failed", "deferred":
				row.SourceState = "failed"
			default:
				continue
			}
			row.Reason.Code = v.State
			row.Reason.Message = v.Reason
			row.Reason.NextAttemptAt = v.NextAttemptAt
			row.Reason.StartedAt = v.StartedAt
			row.Reason.Attempt = v.Attempt
			row.Reason.Actions = []string{"exclude"}
			if v.State == "failed" {
				row.Reason.Actions = []string{"retry", "exclude"}
			}
		}
	}
}
