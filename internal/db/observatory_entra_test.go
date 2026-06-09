package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"agent-relay/internal/db"
)

// fakeCredential is a hand-rolled azcore.TokenCredential that returns a fixed
// token — no Azure SDK network calls.
type fakeCredential struct {
	token string
}

func (f *fakeCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: f.token}, nil
}

func TestWithEntraBeforeConnect_SetsPassword(t *testing.T) {
	fake := &fakeCredential{token: "test-access-token"}
	opt := db.WithEntraBeforeConnect(fake)

	cfg := &pgxpool.Config{
		ConnConfig: &pgx.ConnConfig{},
	}
	opt(cfg)

	if cfg.BeforeConnect == nil {
		t.Fatal("BeforeConnect hook not set")
	}

	connCfg := &pgx.ConnConfig{}
	if err := cfg.BeforeConnect(context.Background(), connCfg); err != nil {
		t.Fatalf("BeforeConnect returned error: %v", err)
	}
	if connCfg.Password != "test-access-token" {
		t.Errorf("password = %q, want %q", connCfg.Password, "test-access-token")
	}
}

func TestWithEntraBeforeConnect_SetsMaxConnLifetime(t *testing.T) {
	fake := &fakeCredential{token: "tok"}
	opt := db.WithEntraBeforeConnect(fake)

	cfg := &pgxpool.Config{
		ConnConfig: &pgx.ConnConfig{},
	}
	opt(cfg)

	const want = 45 * time.Minute
	if cfg.MaxConnLifetime != want {
		t.Errorf("MaxConnLifetime = %v, want %v", cfg.MaxConnLifetime, want)
	}
}
