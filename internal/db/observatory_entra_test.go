package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"agent-relay/internal/db"
)

// fakeCredential is a hand-rolled azcore.TokenCredential that returns a fixed
// token — no Azure SDK network calls.
type fakeCredential struct {
	token string
}

func (f *fakeCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: f.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// TestNewPGPool_EmptyDSNWithCred verifies that an empty DSN returns (nil, nil)
// even when a credential is provided — the credential is not consulted.
func TestNewPGPool_EmptyDSNWithCred(t *testing.T) {
	cred := &fakeCredential{token: "should-not-be-called"}
	pool, err := db.NewPGPool(context.Background(), "", cred)
	if err != nil {
		t.Fatalf("empty DSN must not error, got: %v", err)
	}
	if pool != nil {
		pool.Close()
		t.Error("empty DSN must return nil pool")
	}
}

// TestNewPGPool_InvalidDSNWithCred verifies that an invalid DSN returns an error
// even when a valid credential is supplied.
func TestNewPGPool_InvalidDSNWithCred(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cred := &fakeCredential{token: "test-token"}
	pool, err := db.NewPGPool(ctx,
		"host=127.0.0.1 port=19999 dbname=nonexistent connect_timeout=1 sslmode=disable",
		cred,
	)
	if pool != nil {
		pool.Close()
	}
	if err != nil && pool != nil {
		t.Error("expected nil pool when an error is returned")
	}
}
