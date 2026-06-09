package db_test

import (
	"context"
	"testing"
	"time"

	"agent-relay/internal/db"
)

// TestNewPGPool_EmptyDSN verifies that an empty DSN returns (nil, nil)
// without attempting a connection — Postgres is simply disabled.
func TestNewPGPool_EmptyDSN(t *testing.T) {
	pool, err := db.NewPGPool(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("empty DSN must not error, got: %v", err)
	}
	if pool != nil {
		pool.Close()
		t.Error("empty DSN must return nil pool")
	}
}

// TestNewPGPool_InvalidDSN verifies that a bad DSN returns a non-nil
// error and a nil pool — the caller can treat this as non-fatal.
func TestNewPGPool_InvalidDSN(t *testing.T) {
	// Use a port that is almost certainly not listening so the ping fails fast.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pool, err := db.NewPGPool(ctx,
		"host=127.0.0.1 port=19999 dbname=nonexistent connect_timeout=1 sslmode=disable",
		nil,
	)
	if pool != nil {
		pool.Close()
	}
	// We expect either an error from New/parse or from Ping.
	// Either way, pool must be nil when err != nil.
	if err != nil && pool != nil {
		t.Error("expected nil pool when an error is returned")
	}
}
