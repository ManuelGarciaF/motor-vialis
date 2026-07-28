package config

import (
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
)

func TestFromEnvBuildsDefaultLocalDatabaseURL(t *testing.T) {
	clearDatabaseEnv(t)

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	const want = "postgresql://postgres:postgres@localhost:5432/vialis"
	if cfg.DatabaseURL != want {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
	if _, ok := cfg.SimulationAccessibilityCalculator.(demand.LinearAccessibility); !ok {
		t.Fatalf(
			"SimulationAccessibility type = %T, want LinearAccessibility",
			cfg.SimulationAccessibilityCalculator,
		)
	}
}

func TestFromEnvLoadsAccessibilityMethod(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("SIMULATION_ACCESSIBILITY_METHOD", "quadratic")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if _, ok := cfg.SimulationAccessibilityCalculator.(demand.QuadraticAccessibility); !ok {
		t.Fatalf(
			"SimulationAccessibility type = %T, want QuadraticAccessibility",
			cfg.SimulationAccessibilityCalculator,
		)
	}
}

func TestFromEnvRejectsUnknownAccessibilityMethod(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("SIMULATION_ACCESSIBILITY_METHOD", "unknown")

	if _, err := FromEnv(); err == nil {
		t.Fatal("FromEnv() error = nil, want invalid accessibility method error")
	}
}

func TestFromEnvBuildsDatabaseURLFromComponents(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("DATABASE_HOST", "database.internal")
	t.Setenv("DATABASE_PORT", "5433")
	t.Setenv("DATABASE_NAME", "motor")
	t.Setenv("DATABASE_USER", "vialis")
	t.Setenv("DATABASE_PASSWORD", "p@ss word")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	const want = "postgresql://vialis:p%40ss%20word@database.internal:5433/motor"
	if cfg.DatabaseURL != want {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
}

func TestFromEnvPrefersDatabaseURL(t *testing.T) {
	clearDatabaseEnv(t)
	const want = "postgresql://explicit:secret@server:5432/database"
	t.Setenv("DATABASE_URL", want)
	t.Setenv("DATABASE_USER", "ignored")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.DatabaseURL != want {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
}

func TestFromEnvRejectsInvalidDatabasePort(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("DATABASE_PORT", "invalid")

	if _, err := FromEnv(); err == nil {
		t.Fatal("FromEnv() error = nil, want invalid-port error")
	}
}

func clearDatabaseEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"DATABASE_URL",
		"DATABASE_HOST",
		"DATABASE_PORT",
		"DATABASE_NAME",
		"DATABASE_USER",
		"DATABASE_PASSWORD",
		"SIMULATION_ACCESSIBILITY_METHOD",
	} {
		t.Setenv(name, "")
	}
}
