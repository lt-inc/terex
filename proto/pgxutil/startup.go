// Package pgxutil configures small PostgreSQL pools and waits for startup only.
// It never retries SQL statements or transactions.
package pgxutil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	DefaultStartupTimeout = 60 * time.Second
	DefaultConnectTimeout = 5 * time.Second
)

// ParseConfig preserves PostgreSQL/TLS settings and explicit pool parameters.
// ConnConfig is safe for stdlib.OpenDB: pgxpool strips its own pool_* parameters.
func ParseConfig(dsn string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// The connection parser retains pool parameters before pgxpool consumes them.
	// Using the driver parser also supports keyword DSNs, escapes and service files.
	raw, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if _, explicit := raw.RuntimeParams["pool_max_conns"]; !explicit {
		cfg.MaxConns = 4
	}
	if _, explicit := raw.RuntimeParams["pool_min_conns"]; !explicit {
		cfg.MinConns = 0
	}
	if _, explicit := raw.RuntimeParams["pool_health_check_period"]; !explicit {
		cfg.HealthCheckPeriod = 30 * time.Second
	}
	if cfg.ConnConfig.ConnectTimeout <= 0 {
		cfg.ConnConfig.ConnectTimeout = DefaultConnectTimeout
	}
	if cfg.MinConns > cfg.MaxConns || cfg.MinIdleConns > cfg.MaxConns {
		return nil, errors.New("PostgreSQL minimum pool size exceeds pool_max_conns")
	}
	if cfg.HealthCheckPeriod <= 0 {
		return nil, errors.New("PostgreSQL pool_health_check_period must be positive")
	}
	return cfg, nil
}

// StartupContext bounds callers that did not supply a startup deadline. An
// existing deadline, including the collector's shorter deadline, is preserved.
func StartupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultStartupTimeout)
}

// IsTransientConnectionError classifies connection availability, not transaction
// replay safety. Authentication, database/configuration and TLS certificate
// errors are permanent, including when wrapped by a pgconn.ConnectError.
func IsTransientConnectionError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	transient, permanent := connectionErrorTree(err)
	return transient && !permanent
}

// pgconn joins failures from multiple addresses and TLS fallbacks. A permanent
// authentication/configuration/certificate cause wins over availability errors;
// generic messages (for example a refused TLS fallback) do not block recovery.
func connectionErrorTree(err error) (transient, permanent bool) {
	switch cause := err.(type) {
	case *pgconn.PgError:
		switch cause.Code {
		case "57P03", "57P01", "57P02", "53300", "08000", "08001", "08003", "08006":
			transient = true
		default:
			permanent = true
		}
	case x509.CertificateInvalidError, *x509.CertificateInvalidError,
		x509.UnknownAuthorityError, *x509.UnknownAuthorityError,
		x509.HostnameError, *x509.HostnameError,
		tls.RecordHeaderError, *tls.RecordHeaderError:
		permanent = true
	case net.Error:
		transient = true
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF || err == driver.ErrBadConn || err == pgconn.ErrConnClosed || err == context.DeadlineExceeded {
		transient = true
	}
	var children []error
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children = wrapped.Unwrap()
	case interface{ Unwrap() error }:
		children = []error{wrapped.Unwrap()}
	}
	for _, child := range children {
		if child == nil {
			continue
		}
		retry, stop := connectionErrorTree(child)
		transient = transient || retry
		permanent = permanent || stop
	}
	return transient, permanent
}

// WaitPing retries only the connection check before migrations. Its backoff is
// bounded by the startup context, and permanent failures return immediately.
func WaitPing(ctx context.Context, ping func(context.Context) error) error {
	ctx, cancel := StartupContext(ctx)
	defer cancel()
	delay := 100 * time.Millisecond
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("PostgreSQL startup wait ended: %w", errors.Join(err, last))
		}
		last = ping(ctx)
		if ctx.Err() != nil {
			return fmt.Errorf("PostgreSQL startup wait ended: %w", errors.Join(ctx.Err(), last))
		}
		if last == nil || !IsTransientConnectionError(last) {
			return last
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("PostgreSQL startup wait ended: %w", errors.Join(ctx.Err(), last))
		case <-timer.C:
		}
		if delay < 2*time.Second {
			delay = min(delay*2, 2*time.Second)
		}
	}
}
