package config

import (
	"testing"
)

func TestLoad_ObservatoryDefaults(t *testing.T) {
	t.Setenv("WRAITH_OBSERVATORY_ENABLED", "")
	t.Setenv("WRAITH_OBSERVATORY_DB_URL", "")

	cfg := Load()

	if cfg.ObservatoryEnabled {
		t.Error("ObservatoryEnabled should default to false")
	}
	if cfg.ObservatoryDBURL != "" {
		t.Errorf("ObservatoryDBURL should default to empty, got %q", cfg.ObservatoryDBURL)
	}
}

func TestLoad_ObservatoryEnabled(t *testing.T) {
	t.Setenv("WRAITH_OBSERVATORY_ENABLED", "1")
	t.Setenv("WRAITH_OBSERVATORY_DB_URL", "postgres://user:pass@localhost:5432/observatory")

	cfg := Load()

	if !cfg.ObservatoryEnabled {
		t.Error("ObservatoryEnabled should be true when WRAITH_OBSERVATORY_ENABLED=1")
	}
	const wantURL = "postgres://user:pass@localhost:5432/observatory"
	if cfg.ObservatoryDBURL != wantURL {
		t.Errorf("ObservatoryDBURL: got %q, want %q", cfg.ObservatoryDBURL, wantURL)
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
