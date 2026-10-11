// Package postgres implements the domain repositories on top of pgx.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"terex/main-be/internal/domain"
	"terex/main-be/migrations"
	"terex/proto/pgxutil"
)

type Store struct {
	pool          *pgxpool.Pool
	pressureMu    sync.Mutex
	pressureCount int
	pressureAt    time.Time
	pressureEpoch int64
}

func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxutil.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// Frequent OLTP backlog predicates trigger expensive LLVM compilation on
	// staging, monopolizing the small pool. Keep JIT off for these application
	// sessions by default while honoring an explicitly supplied DSN setting.
	if _, explicit := cfg.ConnConfig.RuntimeParams["jit"]; !explicit {
		cfg.ConnConfig.RuntimeParams["jit"] = "off"
	}
	cfg.ConnConfig.Tracer = otelpgx.NewTracer() // a span per SQL query
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pgxutil.WaitPing(ctx, pool.Ping); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Migrate applies embedded goose migrations.
func Migrate(dsn string) error {
	return MigrateContext(context.Background(), dsn)
}

// MigrateContext waits for availability, then runs migrations exactly once.
func MigrateContext(ctx context.Context, dsn string) error {
	ctx, cancel := pgxutil.StartupContext(ctx)
	defer cancel()
	cfg, err := pgxutil.ParseConfig(dsn)
	if err != nil {
		return err
	}
	db := stdlib.OpenDB(*cfg.ConnConfig)
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := pgxutil.WaitPing(ctx, db.PingContext); err != nil {
		return err
	}
	return migrateDBContext(ctx, db)
}

func migrateDBContext(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, db, ".")
}

// mapErr turns pgx.ErrNoRows into domain.ErrNotFound.
func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
