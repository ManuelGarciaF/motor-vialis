package config

import (
	"reflect"
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
	if !reflect.DeepEqual(
		cfg.SimulationTravelTimePolicy,
		DefaultTravelTimePolicy(),
	) {
		t.Fatalf(
			"SimulationTravelTimePolicy = %#v, want default %#v",
			cfg.SimulationTravelTimePolicy,
			DefaultTravelTimePolicy(),
		)
	}
	if cfg.SimulationRevenueCaptureFactor != 1 || cfg.SimulationRegisteredCardShare != 1 {
		t.Fatalf("revenue defaults = %v, %v; want 1, 1", cfg.SimulationRevenueCaptureFactor, cfg.SimulationRegisteredCardShare)
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

func TestFromEnvLoadsRevenueAssumptions(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("SIMULATION_REVENUE_CAPTURE_FACTOR", "0.65")
	t.Setenv("SIMULATION_REGISTERED_CARD_SHARE", "0.8")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.SimulationRevenueCaptureFactor != 0.65 || cfg.SimulationRegisteredCardShare != 0.8 {
		t.Fatalf("revenue assumptions = %#v", cfg)
	}
}

func TestFromEnvRejectsInvalidRevenueAssumptions(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("SIMULATION_REVENUE_CAPTURE_FACTOR", "1.1")
	if _, err := FromEnv(); err == nil {
		t.Fatal("FromEnv() error = nil")
	}
	clearDatabaseEnv(t)
	t.Setenv("SIMULATION_REGISTERED_CARD_SHARE", "invalid")
	if _, err := FromEnv(); err == nil {
		t.Fatal("FromEnv() error = nil")
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
		"SIMULATION_REVENUE_CAPTURE_FACTOR",
		"SIMULATION_REGISTERED_CARD_SHARE",
	} {
		t.Setenv(name, "")
	}
}
