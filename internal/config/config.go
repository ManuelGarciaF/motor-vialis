package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

const (
	defaultDatabaseHost     = "localhost"
	defaultDatabasePort     = "5432"
	defaultDatabaseName     = "vialis"
	defaultDatabaseUser     = "postgres"
	defaultDatabasePassword = "postgres"

	linearAccessibilityMethod    = "linear"
	quadraticAccessibilityMethod = "quadratic"
	defaultAccessibilityMethod   = linearAccessibilityMethod

	defaultHTTPAddress       = ":8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 10 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second

	defaultSimulationRevenueCaptureFactor = 1.0
	defaultSimulationRegisteredCardShare  = 1.0

	// SimulationAccessRadiusMeters is the maximum walking distance between a
	// stop and a cell's point of maximum concurrence.
	SimulationAccessRadiusMeters = 800.0
)

// Config contains the runtime settings for the HTTP service.
type Config struct {
	HTTPAddress                       string
	DatabaseURL                       string
	SimulationAccessibilityCalculator demand.AccessibilityCalculator
	SimulationTravelTimePolicy        traveltime.Policy
	SimulationRevenueCaptureFactor    float64
	SimulationRegisteredCardShare     float64
	ReadHeaderTimeout                 time.Duration
	ReadTimeout                       time.Duration
	WriteTimeout                      time.Duration
	IdleTimeout                       time.Duration
}

// FromEnv loads configuration from environment variables and applies safe defaults.
func FromEnv() (Config, error) {
	databaseURL, err := databaseURLFromEnv()
	if err != nil {
		return Config{}, err
	}
	accessibilityCalculator, err := accessibilityCalculatorFromEnv()
	if err != nil {
		return Config{}, err
	}
	captureFactor, err := proportionFromEnv(
		"SIMULATION_REVENUE_CAPTURE_FACTOR",
		defaultSimulationRevenueCaptureFactor,
	)
	if err != nil {
		return Config{}, err
	}
	registeredCardShare, err := proportionFromEnv(
		"SIMULATION_REGISTERED_CARD_SHARE",
		defaultSimulationRegisteredCardShare,
	)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddress:                       valueOrDefault("HTTP_ADDRESS", defaultHTTPAddress),
		DatabaseURL:                       databaseURL,
		SimulationAccessibilityCalculator: accessibilityCalculator,
		SimulationTravelTimePolicy:        DefaultTravelTimePolicy(),
		SimulationRevenueCaptureFactor:    captureFactor,
		SimulationRegisteredCardShare:     registeredCardShare,
		ReadHeaderTimeout:                 defaultReadHeaderTimeout,
		ReadTimeout:                       defaultReadTimeout,
		WriteTimeout:                      defaultWriteTimeout,
		IdleTimeout:                       defaultIdleTimeout,
	}

	durations := []struct {
		name   string
		target *time.Duration
	}{
		{name: "HTTP_READ_HEADER_TIMEOUT", target: &cfg.ReadHeaderTimeout},
		{name: "HTTP_READ_TIMEOUT", target: &cfg.ReadTimeout},
		{name: "HTTP_WRITE_TIMEOUT", target: &cfg.WriteTimeout},
		{name: "HTTP_IDLE_TIMEOUT", target: &cfg.IdleTimeout},
	}

	for _, duration := range durations {
		value := os.Getenv(duration.name)
		if value == "" {
			continue
		}

		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration: %q", duration.name, value)
		}
		*duration.target = parsed
	}

	return cfg, nil
}

func accessibilityCalculatorFromEnv() (demand.AccessibilityCalculator, error) {
	method := strings.ToLower(strings.TrimSpace(
		valueOrDefault("SIMULATION_ACCESSIBILITY_METHOD", defaultAccessibilityMethod),
	))
	switch method {
	case linearAccessibilityMethod:
		return demand.LinearAccessibility{}, nil
	case quadraticAccessibilityMethod:
		return demand.QuadraticAccessibility{}, nil
	default:
		return nil, fmt.Errorf(
			"SIMULATION_ACCESSIBILITY_METHOD must be %q or %q: %q",
			linearAccessibilityMethod,
			quadraticAccessibilityMethod,
			method,
		)
	}
}

func proportionFromEnv(name string, defaultValue float64) (float64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 || parsed > 1 {
		return 0, fmt.Errorf("%s must be a number between 0 and 1: %q", name, value)
	}
	return parsed, nil
}

func databaseURLFromEnv() (string, error) {
	if databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL")); databaseURL != "" {
		return databaseURL, nil
	}

	host := strings.TrimSpace(valueOrDefault("DATABASE_HOST", defaultDatabaseHost))
	port := strings.TrimSpace(valueOrDefault("DATABASE_PORT", defaultDatabasePort))
	databaseName := strings.TrimSpace(valueOrDefault("DATABASE_NAME", defaultDatabaseName))
	user := strings.TrimSpace(valueOrDefault("DATABASE_USER", defaultDatabaseUser))
	password := valueOrDefault("DATABASE_PASSWORD", defaultDatabasePassword)

	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return "", fmt.Errorf("DATABASE_PORT must be between 1 and 65535: %q", port)
	}
	if host == "" {
		return "", fmt.Errorf("DATABASE_HOST must not be empty")
	}
	if databaseName == "" {
		return "", fmt.Errorf("DATABASE_NAME must not be empty")
	}
	if user == "" {
		return "", fmt.Errorf("DATABASE_USER must not be empty")
	}

	databaseURL := url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(user, password),
		Host:   net.JoinHostPort(host, port),
		Path:   databaseName,
	}
	return databaseURL.String(), nil
}

func valueOrDefault(name, defaultValue string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return defaultValue
}
