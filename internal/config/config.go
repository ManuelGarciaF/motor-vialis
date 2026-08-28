// Package config holds the parameters of the simulation model, as constants in
// parameters.go, and the handful of settings a deployment owns, read from the
// environment here.
package config

import "os"

const (
	// DefaultDatabaseURL points at the local development database created by
	// sql/init_db.sql.
	DefaultDatabaseURL = "postgresql://postgres:postgres@localhost:5432/vialis"

	// DefaultHTTPAddress listens on every interface, as a container expects.
	DefaultHTTPAddress = ":8080"
)

// Config contains the settings that legitimately differ between the development
// machine and a deployment: where the database is, and where to listen. Every
// other setting is a model parameter and lives in parameters.go.
type Config struct {
	DatabaseURL string
	HTTPAddress string
}

// FromEnv reads the deployment settings, falling back to the local defaults.
// Neither value is parsed here: an unusable database URL is reported by
// postgres.Open and a bad listen address by the server, both at startup and both
// with a better message than this package could produce.
func FromEnv() Config {
	return Config{
		DatabaseURL: valueOrDefault("DATABASE_URL", DefaultDatabaseURL),
		HTTPAddress: valueOrDefault("HTTP_ADDRESS", DefaultHTTPAddress),
	}
}

func valueOrDefault(name, defaultValue string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return defaultValue
}
