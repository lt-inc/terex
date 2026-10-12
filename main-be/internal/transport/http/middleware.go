package http

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
	err    error
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
func (r *statusRecorder) recordError(err error)       { r.err = err }

func recordRequestError(w http.ResponseWriter, err error) {
	for {
		if recorder, ok := w.(interface{ recordError(error) }); ok {
			recorder.recordError(err)
			return
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return
		}
		w = unwrapper.Unwrap()
	}
}

func requestErrorReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.DeadlineExceeded {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return "context_cancelled"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "deadline_exceeded"
	}
	if status.Code(err) == codes.Unavailable {
		return "dependency_unavailable"
	}
	return "request_failed"
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// AccessLog logs one structured line per request (with trace_id via the request context).
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelInfo
		if r.URL.Path == "/healthz" {
			level = slog.LevelDebug
		}
		if rec.status >= 500 {
			level = slog.LevelError
		}
		reason := requestErrorReason(rec.err)
		if reason == "context_cancelled" && errors.Is(r.Context().Err(), context.Canceled) {
			reason = "request_cancelled"
			level = slog.LevelWarn
		}
		attrs := []any{"method", r.Method, "path", r.URL.Path,
			"status", rec.status, "took", time.Since(start).Round(time.Millisecond), "duration_ms", time.Since(start).Milliseconds()}
		if rec.err != nil {
			attrs = append(attrs, "reason_code", reason, "err", rec.err)
		}
		slog.Log(r.Context(), level, "http request", attrs...)
	})
}

// Traced wraps h with OpenTelemetry server spans (health checks are not traced).
func Traced(h http.Handler) http.Handler {
	return otelhttp.NewHandler(h, "main-be",
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" }))
}
