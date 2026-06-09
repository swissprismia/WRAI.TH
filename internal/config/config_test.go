package config

import (
	"testing"
)

func TestLoad_ObservatoryDefaults(t *testing.T) {
	t.Setenv("WRAITH_OBSERVATORY_ENABLED", "")
	t.Setenv("WRAITH_PG_DSN", "")

	cfg := Load()

	if cfg.ObservatoryEnabled {
		t.Error("ObservatoryEnabled should default to false")
	}
	if cfg.PGPool.DSN != "" {
		t.Errorf("PGPool.DSN should default to empty, got %q", cfg.PGPool.DSN)
	}
}

func TestLoad_ObservatoryEnabled(t *testing.T) {
	t.Setenv("WRAITH_OBSERVATORY_ENABLED", "1")
	t.Setenv("WRAITH_PG_DSN", "postgres://user:pass@localhost:5432/postgres?sslmode=require")

	cfg := Load()

	if !cfg.ObservatoryEnabled {
		t.Error("ObservatoryEnabled should be true when WRAITH_OBSERVATORY_ENABLED=1")
	}
	const wantDSN = "postgres://user:pass@localhost:5432/postgres?sslmode=require"
	if cfg.PGPool.DSN != wantDSN {
		t.Errorf("PGPool.DSN: got %q, want %q", cfg.PGPool.DSN, wantDSN)
	}
}

func TestLoad_ObservatoryEnabledFalseOnOtherValues(t *testing.T) {
	for _, val := range []string{"true", "yes", "0", "false"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("WRAITH_OBSERVATORY_ENABLED", val)
			cfg := Load()
			if cfg.ObservatoryEnabled {
				t.Errorf("ObservatoryEnabled should be false for WRAITH_OBSERVATORY_ENABLED=%q", val)
			}
		})
	}
}
