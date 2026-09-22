// Package config defines model parameters and environment-owned settings.
package config

import "os"

const (
	// DefaultDatabaseURL points at the local development database created by
	// sql/init_db.sql.
	DefaultDatabaseURL = "postgresql://postgres:postgres@localhost:5432/vialis"

	// DefaultHTTPAddress listens on every interface, as a container expects.
	DefaultHTTPAddress = ":8080"
)

// Config contains deployment-specific settings and provider credentials.
type Config struct {
	DatabaseURL  string
	HTTPAddress  string
	TomTomAPIKey string
}

// FromEnv reads deployment settings, using local defaults where available.
func FromEnv() Config {
	return Config{
		DatabaseURL:  valueOrDefault("DATABASE_URL", DefaultDatabaseURL),
		HTTPAddress:  valueOrDefault("HTTP_ADDRESS", DefaultHTTPAddress),
		TomTomAPIKey: os.Getenv("TOMTOM_API_KEY"),
	}
}

func valueOrDefault(name, defaultValue string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return defaultValue
}
