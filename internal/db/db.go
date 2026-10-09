// Package db holds the Postgres pool, migrations, and generated queries.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store wraps the pool and the generated queries.
type Store struct {
	Pool *pgxpool.Pool
	*sqlc.Queries
}

func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{Pool: pool, Queries: sqlc.New(pool)}, nil
}

func (s *Store) Close() { s.Pool.Close() }

// Migrate applies all pending migrations.
func (s *Store) Migrate(ctx context.Context) error {
	sqlDB := stdlib.OpenDBFromPool(s.Pool)
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, mustSub(migrations, "migrations"))
	if err != nil {
		return fmt.Errorf("migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// InTx runs fn in a transaction, committing on nil error.
func (s *Store) InTx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(s.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Ping checks the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

// IsNotFound reports whether err means no row was found.
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
