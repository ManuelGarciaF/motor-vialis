package config

import "testing"

func TestFromEnvFallsBackToLocalDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("HTTP_ADDRESS", "")

	cfg := FromEnv()
	if cfg.DatabaseURL != DefaultDatabaseURL {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, DefaultDatabaseURL)
	}
	if cfg.HTTPAddress != DefaultHTTPAddress {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, DefaultHTTPAddress)
	}
}

func TestFromEnvReadsEnvironment(t *testing.T) {
	const (
		databaseURL = "postgresql://explicit:secret@server:5432/database"
		httpAddress = "127.0.0.1:9090"
	)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("HTTP_ADDRESS", httpAddress)

	cfg := FromEnv()
	if cfg.DatabaseURL != databaseURL {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, databaseURL)
	}
	if cfg.HTTPAddress != httpAddress {
		t.Fatalf("HTTPAddress = %q, want %q", cfg.HTTPAddress, httpAddress)
	}
}

// A default page larger than the maximum would silently return fewer lines than
// configured. The parameters are constants now, so this is caught here rather
// than at startup.
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

// The corridor search shares lines.Policy but answers a different question,
// so its knobs get their own coherence check.
func TestSimilarityPolicyIsCoherent(t *testing.T) {
	policy := LinesPolicy()
	if policy.SimilarityDefaultResultCount > policy.SimilarityMaximumResultCount {
		t.Fatalf(
			"SimilarityDefaultResultCount = %d, must not exceed "+
				"SimilarityMaximumResultCount = %d",
			policy.SimilarityDefaultResultCount,
			policy.SimilarityMaximumResultCount,
		)
	}
	if policy.SimilarityDefaultResultCount <= 0 {
		t.Fatalf(
			"SimilarityDefaultResultCount = %d, must be positive",
			policy.SimilarityDefaultResultCount,
		)
	}
	// A minimum of zero would report every line that so much as touches the
	// route, and one above 1 would report none: neither is a threshold.
	if policy.SimilarityMinimumCoverage <= 0 || policy.SimilarityMinimumCoverage > 1 {
		t.Fatalf(
			"SimilarityMinimumCoverage = %v, must be within (0, 1]",
			policy.SimilarityMinimumCoverage,
		)
	}
	if policy.SimilarityCorridorToleranceMeters <= 0 {
		t.Fatalf(
			"SimilarityCorridorToleranceMeters = %v, must be positive",
			policy.SimilarityCorridorToleranceMeters,
		)
	}
}

// SimulationTimeout must stay below WriteTimeout so a slow simulation is
// answered with a timeout status instead of having its connection closed
// mid-response.
func TestSimulationTimeoutLeavesRoomToRespond(t *testing.T) {
	if SimulationTimeout >= WriteTimeout {
		t.Fatalf(
			"SimulationTimeout = %s, must be below WriteTimeout = %s",
			SimulationTimeout,
			WriteTimeout,
		)
	}
}
