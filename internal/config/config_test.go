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
		databaseURL  = "******server:5432/database"
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

func TestTransfersPolicyIsCoherent(t *testing.T) {
	policy := CombinacionesPolicy()

	if policy.DefaultPageSize <= 0 {
		t.Fatalf("DefaultPageSize = %d, must be positive", policy.DefaultPageSize)
	}
	if policy.DefaultPageSize > policy.MaximumPageSize {
		t.Fatalf(
			"DefaultPageSize = %d, must not exceed MaximumPageSize = %d",
			policy.DefaultPageSize,
			policy.MaximumPageSize,
		)
	}
	// A threshold at or below 1 would warn about every combination, including
	// the ones a flow had no alternative to, which is the strongest claim the
	// ranking can make.
	if policy.WeakEvidenceAlternatives <= 1 {
		t.Fatalf(
			"WeakEvidenceAlternatives = %v, must be greater than 1",
			policy.WeakEvidenceAlternatives,
		)
	}
	// And one at or above the ETL's ceiling could never fire, because no
	// combination above it survives the aggregation. Both ends turn the mark
	// into decoration, so the constant is pinned inside the surviving range.
	if policy.WeakEvidenceAlternatives >= etlAlternativesCeiling {
		t.Fatalf(
			"WeakEvidenceAlternatives = %v, must stay below the %v feasible "+
				"combinations sql/viajes/combinaciones_lineas.sql allows, or "+
				"it can never fire",
			policy.WeakEvidenceAlternatives,
			etlAlternativesCeiling,
		)
	}
}

// etlAlternativesCeiling mirrors the HAVING in
// sql/viajes/combinaciones_lineas.sql. It is repeated here rather than shared
// because the two live in different languages; the test above is what keeps
// them from drifting apart silently.
const etlAlternativesCeiling = 10.0
