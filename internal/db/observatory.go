package db

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/jackc/pgx/v5"
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
func NewObservatoryDB(ctx context.Context, dsn string, opts ...func(*pgxpool.Config)) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, nil
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("observatory db: parse config: %w", err)
	}

	for _, opt := range opts {
		opt(cfg)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("observatory db: open pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("observatory db: ping: %w", err)
	}

	return pool, nil
}

// WithEntraBeforeConnect returns a pool config option that installs an Azure
// Entra token-refresh hook. Before each new physical connection the hook
// fetches a fresh access token from the given credential and injects it as
// the Postgres password. MaxConnLifetime is set to 45 minutes so connections
// are recycled well before token expiry.
func WithEntraBeforeConnect(cred azcore.TokenCredential) func(*pgxpool.Config) {
	return func(cfg *pgxpool.Config) {
		cfg.MaxConnLifetime = 45 * time.Minute
		cfg.BeforeConnect = func(ctx context.Context, connCfg *pgx.ConnConfig) error {
			tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
				Scopes: []string{"https://ossrdbms-aad.database.windows.net/.default"},
			})
			if err != nil {
				return fmt.Errorf("entra: get token: %w", err)
			}
			connCfg.Password = tok.Token
			return nil
		}
	}
}
