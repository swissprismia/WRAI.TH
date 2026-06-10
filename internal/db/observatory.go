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

// NewPGPool opens WRAI.TH's primary Postgres connection pool.
//
// If dsn is empty the function returns (nil, nil) — no pool, no error.
// The caller must treat a nil pool as "Postgres unavailable" and return
// HTTP 503 on affected routes; the relay/chat stack keeps running normally.
//
// When cred is non-nil, a BeforeConnect hook installs an Azure Entra token
// before each new physical connection. MaxConnLifetime is set to 45 minutes
// so connections are recycled well before token expiry.
//
// If the pool cannot be created or the initial ping fails, an error is
// returned with a nil pool. The error is NON-FATAL: the caller must log it
// and continue rather than exiting.
func NewPGPool(ctx context.Context, dsn string, cred azcore.TokenCredential) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, nil
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("pg pool: parse config: %w", err)
	}

	if cred != nil {
		cfg.MaxConnLifetime = 45 * time.Minute
		cfg.BeforeConnect = func(ctx context.Context, connCfg *pgx.ConnConfig) error {
			tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
				Scopes: []string{"https://ossrdbms-aad.database.windows.net/.default"},
			})
			if err != nil {
				return fmt.Errorf("pg pool: get entra token: %w", err)
			}
			connCfg.Password = tok.Token
			return nil
		}
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pg pool: open: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg pool: ping: %w", err)
	}

	return pool, nil
}
