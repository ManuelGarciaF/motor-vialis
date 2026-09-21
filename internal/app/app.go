// Package app is the shared composition root for command binaries.
package app

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
	"github.com/ManuelGarciaF/vialis-motor/internal/traffic/tomtom"
)

// NewSimulationService wires the simulation estimators to PostgreSQL.
func NewSimulationService(database *pgxpool.Pool) *simulation.Service {
	return simulation.NewService(
		demand.NewService(
			postgres.NewDemandRepository(database),
			config.AccessRadiusMeters,
			config.AccessibilityCalculator(),
		),
		traveltime.NewService(
			postgres.NewTravelTimeRepository(database),
			config.TravelTimePolicy(),
		),
		revenue.NewService(
			postgres.NewRevenueRepository(database),
			config.RevenuePolicy(),
		),
	)
}

// NewTomTomTrafficClient creates the process-wide RF05 traffic client. The API
// treats a missing key as a startup configuration error.
func NewTomTomTrafficClient(apiKey string, logger *slog.Logger) (*tomtom.Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("TOMTOM_API_KEY is required")
	}
	return tomtom.NewClient(tomtom.Options{
		APIKey:            apiKey,
		RequestTimeout:    config.TomTomRequestTimeout,
		CacheTTL:          config.TomTomTrafficTTL,
		CacheEntries:      config.TomTomCacheEntries,
		CacheBytes:        config.TomTomCacheBytes,
		RequestsPerSecond: config.TomTomRequestsPerSecond,
		MaximumTiles:      config.DetourMaximumTrafficTiles,
		Margin:            config.TomTomTileMargin,
		Limits: tomtom.Limits{
			MaximumTileBytes: config.TomTomMaximumTileBytes,
			MaximumFeatures:  config.TomTomMaximumTileFeatures,
		},
		Logger: logger,
	})
}

// NewDetourService wires RF05 to PostgreSQL, the shared traffic client, and
// the stable simulation comparator.
func NewDetourService(
	database *pgxpool.Pool,
	trafficProvider detour.TrafficProvider,
	comparator detour.Comparator,
) (*detour.Service, error) {
	return detour.NewService(
		postgres.NewDetourRepository(database),
		trafficProvider,
		comparator,
		config.DetourPolicy(),
	)
}

// NewLinesService wires stored-line export to PostgreSQL.
func NewLinesService(database *pgxpool.Pool) *lines.Service {
	return lines.NewService(
		postgres.NewLinesRepository(database),
		config.LinesPolicy(),
	)
}
