// Package postgres implements the domain repositories on top of pgx.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
	controlPool   *pgxpool.Pool
	pressureMu    sync.Mutex
	pressureCount int
	pressureAt    time.Time
	pressureEpoch int64
	found         foundCache
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
	// The default of 4 connections let slow collector/monitoring queries (several
	// seconds each on a small disk) starve the admin UI of a free connection.
	if raw := os.Getenv("DB_MAX_CONNS"); raw != "" {
		n, convErr := strconv.ParseInt(raw, 10, 32)
		if convErr != nil || n < 2 {
			return nil, fmt.Errorf("DB_MAX_CONNS must be an integer >= 2")
		}
		cfg.MaxConns = int32(n)
	}
	reserved := int64(1)
	if raw := os.Getenv("DB_CONTROL_POOL_RESERVED_CONNS"); raw != "" {
		reserved, err = strconv.ParseInt(raw, 10, 32)
		if err != nil || reserved < 0 {
			return nil, fmt.Errorf("DB_CONTROL_POOL_RESERVED_CONNS must be a non-negative integer")
		}
	}
	workCfg, controlCfg := splitControlPool(cfg, int32(reserved))
	pool, err := pgxpool.NewWithConfig(ctx, workCfg)
	if err != nil {
		return nil, err
	}
	if err := pgxutil.WaitPing(ctx, pool.Ping); err != nil {
		pool.Close()
		return nil, err
	}
	control := pool
	if controlCfg != nil {
		control, err = pgxpool.NewWithConfig(ctx, controlCfg)
		if err == nil {
			err = pgxutil.WaitPing(ctx, control.Ping)
		}
		if err != nil {
			if control != nil {
				control.Close()
			}
			pool.Close()
			return nil, err
		}
	}
	slog.InfoContext(ctx, "PostgreSQL connection budget", "total_max", cfg.MaxConns, "work_max", workCfg.MaxConns, "control_max", cfg.MaxConns-workCfg.MaxConns)
	if reserved > 0 && controlCfg == nil {
		slog.WarnContext(ctx, "PostgreSQL control isolation disabled", "reason_code", "pool_budget_too_small", "total_max", cfg.MaxConns)
	}
	return &Store{pool: pool, controlPool: control}, nil
}

func splitControlPool(cfg *pgxpool.Config, reserved int32) (*pgxpool.Config, *pgxpool.Config) {
	work := cfg.Copy()
	reserved = min(reserved, max(0, cfg.MaxConns-1))
	if reserved == 0 {
		return work, nil
	}
	control := cfg.Copy()
	work.MaxConns = cfg.MaxConns - reserved
	control.MaxConns = reserved
	work.MinConns = min(cfg.MinConns, work.MaxConns)
	control.MinConns = cfg.MinConns - work.MinConns
	work.MinIdleConns = min(cfg.MinIdleConns, work.MaxConns)
	control.MinIdleConns = cfg.MinIdleConns - work.MinIdleConns
	return work, control
}

func (s *Store) controlDB() *pgxpool.Pool {
	if s.controlPool != nil {
		return s.controlPool
	}
	return s.pool
}

func (s *Store) Close() {
	if s.controlPool != nil && s.controlPool != s.pool {
		s.controlPool.Close()
	}
	s.pool.Close()
}

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
