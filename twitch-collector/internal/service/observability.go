package service

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"terex/twitch-collector/internal/domain"
)

type gateLog struct {
	reason     string
	at         time.Time
	observedAt time.Time
}

func settingsBlockReason(cfg domain.Settings, err error, credentialsReady bool) string {
	if err != nil {
		return "Terex settings unavailable: " + safeErrorMessage(err, "check backend connectivity and service token")
	}
	if cfg.TargetAccountID <= 0 {
		return "Choose a connected YouTube account and enable collection"
	}
	if !cfg.Enabled {
		return "Collector is paused; enable collection and save settings"
	}
	if !credentialsReady {
		return "Twitch application credentials are not configured"
	}
	return ""
}

// State changes are immediately visible; a continuing problem is repeated at
// most once every five minutes, even though the worker polls every five seconds.
func (s *Service) logGate(ctx context.Context, component, reason string, err error) bool {
	now := time.Now()
	s.mu.Lock()
	previous, exists := s.gateLogs[component]
	if exists && previous.reason == reason && (reason == "" || now.Sub(previous.at) < 5*time.Minute) {
		previous.observedAt = now
		s.gateLogs[component] = previous
		s.mu.Unlock()
		return false
	}
	s.gateLogs[component] = gateLog{reason: reason, at: now, observedAt: now}
	s.mu.Unlock()
	if reason == "" {
		if exists && previous.reason != "" {
			s.log.InfoContext(ctx, "collector resumed", "component", component)
		}
		return false
	}
	fields := []any{"component", component, "reason", reason}
	if err != nil {
		fields = append(fields, errorFields(err)...)
		s.log.WarnContext(ctx, "collector waiting", fields...)
	} else {
		s.log.InfoContext(ctx, "collector waiting", fields...)
	}
	return true
}

// Provider adapters return fixed diagnostics and HTTP status codes. PostgreSQL's
// primary message describes the failed query; Detail and query values are omitted.
// Unknown errors can contain DSNs or signed URLs and are not printed.
func safeErrorMessage(err error, fallback string) string {
	var failure *domain.Failure
	if errors.As(err, &failure) {
		return failure.Message
	}
	var storageError *domain.StorageError
	prefix := ""
	if errors.As(err, &storageError) {
		prefix = storageError.Operation + ": "
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		return prefix + "PostgreSQL " + pgError.Code + ": " + pgError.Message
	}
	if errors.Is(err, context.Canceled) {
		return prefix + "operation canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return prefix + "operation timed out"
	}
	return prefix + fallback
}

func sanitizedError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(safeErrorMessage(err, "collector operation failed"))
}

func errorFields(err error) []any {
	var storageError *domain.StorageError
	fields := []any{}
	if errors.As(err, &storageError) {
		fields = append(fields, "operation", storageError.Operation)
	}
	var failure *domain.Failure
	if errors.As(err, &failure) {
		fields = append(fields, "error_kind", failure.Kind, "err", failure.Message)
		if failure.Code != "" {
			fields = append(fields, "reason_code", failure.Code)
		}
		if failure.Operation != "" {
			fields = append(fields, "operation", failure.Operation, "duration_ms", failure.DurationMS)
		}
		if failure.HTTPStatus != 0 {
			fields = append(fields, "http_status", failure.HTTPStatus)
		}
		return fields
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		return append(fields, "error_kind", "database", "sql_state", pgError.Code, "err", safeErrorMessage(err, "database request failed"))
	}
	return append(fields, "error_kind", "internal", "err", safeErrorMessage(err, "operation unavailable"))
}
