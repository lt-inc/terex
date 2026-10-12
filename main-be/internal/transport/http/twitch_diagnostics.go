package http

import (
	"context"
	"errors"
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
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			out.Collector.Message = "Подробная диагностика collector не ответила за 3 секунды. Это не означает остановку скачивания. Бюджет показан по резервам main-be"
		} else {
			out.Collector.Message = "Подробная диагностика collector временно не получена: " + err.Error() + ". Это не означает остановку скачивания. Бюджет показан по резервам main-be"
		}
	} else {
		collector.Available = true
		twitchmonitor.Enrich(&out, collector)
		observedAt := time.Now().UTC()
		out.Collector.ObservedAt = &observedAt
	}
	writeJSON(w, http.StatusOK, out)
}
