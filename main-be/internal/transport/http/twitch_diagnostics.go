package http

import (
	"context"
	"github.com/go-chi/chi/v5"
	"net/http"
	"net/url"
	"terex/main-be/internal/domain"
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
		pending := map[string]bool{}
		for _, clip := range out.PendingClipIDs {
			pending[clip] = true
		}
		for _, item := range collector.Clips {
			if !pending[item.ClipID] {
				continue
			}
			state := item.State
			switch state {
			case "downloading", "uploading":
				state = "downloading"
			case "importing", "verifying":
				state = "verifying"
			case "retry_wait":
				state = "retry"
			case "queued", "reserved", "waiting", "waiting_quota", "waiting_capacity":
				state = "queued"
			case "failed", "deferred":
				state = "failed"
			default:
				continue
			}
			if previous := out.PendingStates[item.ClipID]; previous != "" && out.Sources[previous] > 0 {
				out.Sources[previous]--
			}
			out.Sources[state]++
		}
		out.Collector.Available = true
		out.Collector.Discovery = collector.Discovery
		if out.EstimatedAdditionalClips > out.Collector.Remaining && out.Feasibility == "needs_sources" {
			out.Feasibility = "budget_insufficient"
			out.Messages = append(out.Messages, "Оставшегося общего дневного бюджета может не хватить на подборку. Сократите минимальную длину или увеличьте бюджет; оценка не гарантирует доступность подходящих клипов")
		}
	}
	writeJSON(w, http.StatusOK, out)
}
