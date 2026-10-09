package http

import (
	"context"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/url"
	"terex/main-be/internal/domain"
	"terex/main-be/internal/service/twitchmonitor"
	"time"
)

func (a *API) twitchFlowDiagnostics(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := a.Twitch.Diagnostics(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var collector domain.TwitchDiagnosticCollector
	if err = a.Collector.call(ctx, http.MethodGet, "flows/"+url.PathEscape(id)+"/diagnostics", nil, &collector); err != nil {
		out.Collector.Message = "Collector недоступен: подробные состояния скачивания временно неизвестны. Бюджет показан по резервам main-be"
	} else {
		collector.Available = true
		twitchmonitor.Enrich(&out, collector)
	}
	writeJSON(w, http.StatusOK, out)
}
