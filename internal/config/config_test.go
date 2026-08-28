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

func TestFromEnvLoadsTheDefaultLinesPolicy(t *testing.T) {
	clearDatabaseEnv(t)
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.LinesPolicy != DefaultLinesPolicy() {
		t.Fatalf("lines policy = %#v, want %#v", cfg.LinesPolicy, DefaultLinesPolicy())
	}
}

func TestFromEnvLoadsTheLinesPolicy(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("LINES_ALIGNMENT_TOLERANCE_METERS", "120.5")
	t.Setenv("LINES_DEFAULT_PAGE_SIZE", "20")
	t.Setenv("LINES_MAXIMUM_PAGE_SIZE", "40")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if cfg.LinesPolicy.AlignmentToleranceMeters != 120.5 {
		t.Fatalf("tolerance = %v, want 120.5", cfg.LinesPolicy.AlignmentToleranceMeters)
	}
	if cfg.LinesPolicy.DefaultPageSize != 20 || cfg.LinesPolicy.MaximumPageSize != 40 {
		t.Fatalf("page sizes = %#v, want 20/40", cfg.LinesPolicy)
	}
}

func TestFromEnvRejectsAnInvalidLinesPolicy(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"zero tolerance", "LINES_ALIGNMENT_TOLERANCE_METERS", "0"},
		{"negative tolerance", "LINES_ALIGNMENT_TOLERANCE_METERS", "-1"},
		{"non numeric tolerance", "LINES_ALIGNMENT_TOLERANCE_METERS", "wide"},
		{"zero page size", "LINES_DEFAULT_PAGE_SIZE", "0"},
		{"non numeric page size", "LINES_MAXIMUM_PAGE_SIZE", "all"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			clearDatabaseEnv(t)
			t.Setenv(testCase.key, testCase.value)
			if _, err := FromEnv(); err == nil {
				t.Fatalf("FromEnv() error = nil for %s=%q", testCase.key, testCase.value)
			}
		})
	}
}

// A default page larger than the maximum would silently return fewer lines
// than configured, so it is refused at startup instead.
func TestFromEnvRejectsADefaultPageLargerThanTheMaximum(t *testing.T) {
	clearDatabaseEnv(t)
	t.Setenv("LINES_DEFAULT_PAGE_SIZE", "100")
	t.Setenv("LINES_MAXIMUM_PAGE_SIZE", "50")

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
