package postgres

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestMigrateContextWithPoolParameters(t *testing.T) {
	dsn := testSchema(t)
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("pool_max_conns", "2")
	query.Set("pool_min_conns", "0")
	query.Set("pool_health_check_period", "10s")
	query.Set("application_name", "migration-startup-test")
	u.RawQuery = query.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = MigrateContext(ctx, u.String()); err != nil {
		t.Fatal("migration failed with pool-only DSN parameters", err)
	}
	s, err := New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var migrated, session bool
	if err = s.pool.QueryRow(ctx, "SELECT to_regclass('publications') IS NOT NULL,current_setting('application_name')='migration-startup-test'").Scan(&migrated, &session); err != nil || !migrated || !session {
		t.Fatal("schema or session settings missing after startup", migrated, session, err)
	}
	total := s.pool.Config().MaxConns
	if s.controlDB() != s.pool {
		total += s.controlDB().Config().MaxConns
		if err = s.controlDB().QueryRow(ctx, "SELECT current_setting('application_name')='migration-startup-test'").Scan(&session); err != nil || !session {
			t.Fatal("control pool lost explicit session settings", session, err)
		}
	}
	if total != 2 {
		t.Fatal("explicit total pool maximum changed")
	}
}

func TestCanceledStartupDoesNotRunMigrations(t *testing.T) {
	dsn := testSchema(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := MigrateContext(ctx, dsn); !errors.Is(err, context.Canceled) {
		t.Fatalf("migration ignored cancelled startup: %v", err)
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var count int
	if err = conn.QueryRow(context.Background(), "SELECT count(*) FROM pg_tables WHERE schemaname=current_schema()").Scan(&count); err != nil || count != 0 {
		t.Fatal("cancelled startup applied a migration", count, err)
	}
}
