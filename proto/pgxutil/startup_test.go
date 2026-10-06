package pgxutil

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestParseConfigDefaultsAndTLSFallback(t *testing.T) {
	cfg, err := ParseConfig("postgres://user:fake-password@127.0.0.1/test?sslmode=prefer&application_name=terex-test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != 4 || cfg.MinConns != 0 || cfg.HealthCheckPeriod != 30*time.Second || cfg.ConnConfig.ConnectTimeout != 5*time.Second {
		t.Fatalf("unexpected defaults: max=%d min=%d health=%s connect=%s", cfg.MaxConns, cfg.MinConns, cfg.HealthCheckPeriod, cfg.ConnConfig.ConnectTimeout)
	}
	if cfg.ConnConfig.TLSConfig == nil || len(cfg.ConnConfig.Fallbacks) == 0 || cfg.ConnConfig.Fallbacks[0].TLSConfig != nil {
		t.Fatal("sslmode=prefer TLS and plaintext fallback were changed")
	}
	if cfg.ConnConfig.Password != "fake-password" || cfg.ConnConfig.RuntimeParams["application_name"] != "terex-test" {
		t.Fatal("credentials or PostgreSQL session parameters were changed")
	}
}

func TestParseConfigPreservesExplicitDSNAndSanitizesMigrationConfig(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:fake-password@127.0.0.1/test?sslmode=disable&connect_timeout=9&pool_max_conns=7&pool_min_conns=1&pool_min_idle_conns=1&pool_health_check_period=7s&pool_max_conn_lifetime=13m&search_path=isolated",
		"host=127.0.0.1 user=user password='fake password' dbname=test sslmode=disable connect_timeout=9 pool_max_conns=7 pool_min_conns=1 pool_min_idle_conns=1 pool_health_check_period=7s pool_max_conn_lifetime=13m search_path=isolated",
	} {
		cfg, err := ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.MaxConns != 7 || cfg.MinConns != 1 || cfg.MinIdleConns != 1 || cfg.HealthCheckPeriod != 7*time.Second || cfg.MaxConnLifetime != 13*time.Minute || cfg.ConnConfig.ConnectTimeout != 9*time.Second {
			t.Fatal("explicit connection/pool settings were replaced")
		}
		if cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 || cfg.ConnConfig.RuntimeParams["search_path"] != "isolated" {
			t.Fatal("TLS or search_path changed")
		}
		for name := range cfg.ConnConfig.RuntimeParams {
			if strings.HasPrefix(name, "pool_") {
				t.Fatalf("migration would send a pool parameter to PostgreSQL: %s", name)
			}
		}
	}
}

func TestParseConfigRejectsInvalidPool(t *testing.T) {
	for _, query := range []string{"pool_min_conns=5", "pool_health_check_period=0s", "pool_health_check_period=-1s", "pool_max_conns=invalid"} {
		if _, err := ParseConfig("postgres://user@127.0.0.1/test?sslmode=disable&" + query); err == nil {
			t.Fatalf("invalid configuration accepted: %s", query)
		}
	}
}

func TestTransientConnectionClassification(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"shutdown", &pgconn.PgError{Code: "57P03"}, true},
		{"admin shutdown", &pgconn.PgError{Code: "57P01"}, true},
		{"connection slots exhausted", &pgconn.PgError{Code: "53300"}, true},
		{"closed connection", fmt.Errorf("ping: %w", pgconn.ErrConnClosed), true},
		{"wrapped refusal", fmt.Errorf("connect: %w", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}), true},
		{"TLS refusal followed by shutdown", errors.Join(errors.New("server refused TLS connection"), &pgconn.PgError{Code: "57P03"}), true},
		{"shutdown joined with password failure", errors.Join(&pgconn.PgError{Code: "57P03"}, &pgconn.PgError{Code: "28P01"}), false},
		{"wrapped shutdown joined with password failure", fmt.Errorf("connect: %w", errors.Join(&pgconn.PgError{Code: "57P03"}, &pgconn.PgError{Code: "28P01"})), false},
		{"shutdown joined with invalid certificate", errors.Join(&pgconn.PgError{Code: "57P03"}, x509.UnknownAuthorityError{}), false},
		{"invalid certificate joined with shutdown", errors.Join(x509.UnknownAuthorityError{}, &pgconn.PgError{Code: "57P03"}), false},
		{"EOF", io.EOF, true},
		{"password", fmt.Errorf("connect: %w", &pgconn.PgError{Code: "28P01"}), false},
		{"missing database", &pgconn.PgError{Code: "3D000"}, false},
		{"invalid runtime parameter", &pgconn.PgError{Code: "42704"}, false},
		{"unknown certificate authority", &net.OpError{Op: "remote error", Net: "tcp", Err: x509.UnknownAuthorityError{}}, false},
		{"cancellation", fmt.Errorf("connect: %w", context.Canceled), false},
		{"other error", errors.New("unexpected result"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsTransientConnectionError(test.err); got != test.want {
				t.Fatalf("transient=%v, want %v", got, test.want)
			}
		})
	}
}

func TestWaitPingRecoversWithoutRepeatingPermanentErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	attempts := 0
	err := WaitPing(ctx, func(context.Context) error {
		attempts++
		switch attempts {
		case 1:
			return &pgconn.PgError{Code: "57P03"}
		case 2:
			return io.EOF
		default:
			return nil
		}
	})
	if err != nil || attempts != 3 {
		t.Fatalf("startup recovery: attempts=%d err=%v", attempts, err)
	}
	attempts = 0
	permanent := &pgconn.PgError{Code: "28P01"}
	err = WaitPing(ctx, func(context.Context) error { attempts++; return permanent })
	if !errors.Is(err, permanent) || attempts != 1 {
		t.Fatalf("authentication must fail immediately: attempts=%d err=%v", attempts, err)
	}
}

func TestWaitPingHonorsCancellationAndDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitPing(ctx, func(context.Context) error { t.Fatal("ping after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled startup did not return cancellation")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	last := &pgconn.PgError{Code: "57P03"}
	started := time.Now()
	err := WaitPing(ctx, func(context.Context) error { return last })
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, last) || time.Since(started) > time.Second {
		t.Fatalf("startup deadline or last failure lost: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- WaitPing(ctx, func(context.Context) error { close(entered); return io.EOF }) }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("waiting startup did not return cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("startup ignored cancellation during backoff")
	}
}

func TestStartupContextPreservesCallerDeadline(t *testing.T) {
	parent, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	ctx, cancel := StartupContext(parent)
	defer cancel()
	expected, _ := parent.Deadline()
	actual, _ := ctx.Deadline()
	if !actual.Equal(expected) {
		t.Fatal("collector startup deadline changed")
	}
	ctx, cancel = StartupContext(context.Background())
	defer cancel()
	actual, ok := ctx.Deadline()
	if !ok || time.Until(actual) > DefaultStartupTimeout || time.Until(actual) < DefaultStartupTimeout-time.Second {
		t.Fatal("unbounded startup context")
	}
}
