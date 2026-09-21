package config

import "testing"

func TestFromEnvFallsBackToLocalDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("HTTP_ADDRESS", "")
	t.Setenv("TOMTOM_API_KEY", "")

	cfg := FromEnv()
	if cfg.DatabaseURL != DefaultDatabaseURL {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, DefaultDatabaseURL)
	}
	if cfg.HTTPAddress != DefaultHTTPAddress {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, DefaultHTTPAddress)
	}
	if cfg.TomTomAPIKey != "" {
		t.Fatalf("TomTomAPIKey = %q, want empty", cfg.TomTomAPIKey)
	}
}

func TestFromEnvReadsEnvironment(t *testing.T) {
	const (
		databaseURL  = "postgresql://explicit:secret@server:5432/database"
		httpAddress  = "127.0.0.1:9090"
		tomTomAPIKey = "test-tomtom-key"
	)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("HTTP_ADDRESS", httpAddress)
	t.Setenv("TOMTOM_API_KEY", tomTomAPIKey)

	cfg := FromEnv()
	if cfg.DatabaseURL != databaseURL {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, databaseURL)
	}
	if cfg.HTTPAddress != httpAddress {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, httpAddress)
	}
	if cfg.TomTomAPIKey != tomTomAPIKey {
		t.Fatalf("TomTomAPIKey = %q, want configured value", cfg.TomTomAPIKey)
	}
}

// The configured default must fit within the maximum page size.
func TestLinesPolicyIsCoherent(t *testing.T) {
	policy := LinesPolicy()
	if policy.DefaultPageSize > policy.MaximumPageSize {
		t.Fatalf(
			"DefaultPageSize = %d, must not exceed MaximumPageSize = %d",
			policy.DefaultPageSize,
			policy.MaximumPageSize,
		)
	}
	if policy.DefaultPageSize <= 0 || policy.AlignmentToleranceMeters <= 0 {
		t.Fatalf("lines policy must be positive: %#v", policy)
	}
}

func TestTomTomPolicyIsCoherent(t *testing.T) {
	if TomTomTrafficTTL <= 0 || TomTomRequestTimeout <= 0 ||
		TomTomCacheEntries <= 0 || TomTomCacheBytes <= 0 ||
		TomTomMaximumTileBytes <= 0 || TomTomRequestsPerSecond <= 0 ||
		TomTomMaximumTileFeatures <= 0 ||
		TomTomTileMargin < 0 || TomTomTileMargin > 0.1 {
		t.Fatalf("invalid TomTom policy")
	}
}

// The handler needs time to report a simulation timeout before writes close.
func TestSimulationTimeoutLeavesRoomToRespond(t *testing.T) {
	if SimulationTimeout >= WriteTimeout {
		t.Fatalf(
			"SimulationTimeout = %s, must be below WriteTimeout = %s",
			SimulationTimeout,
			WriteTimeout,
		)
	}
}
