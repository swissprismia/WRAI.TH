package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewObservatoryDB opens a Postgres connection pool for observatory data.
//
// If dsn is empty the function returns (nil, nil) — no pool, no error.
// The caller must treat a nil pool as "observatory unavailable" and return
// HTTP 503 on affected routes; the relay/chat stack keeps running normally.
//
// If the pool cannot be created or the initial ping fails, an error is
// returned with a nil pool. The error is NON-FATAL: the caller must log it
// and continue rather than exiting.
func NewObservatoryDB(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, nil
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("observatory db: open pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("observatory db: ping: %w", err)
	}

	return pool, nil
}
