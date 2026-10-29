// Package app is the shared composition root for command binaries.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
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

// NewDetourTrafficProvider permits a missing key only in demo mode. The demo
// service turns this explicit provider error into a generic-speed estimate.
func NewDetourTrafficProvider(apiKey string, logger *slog.Logger, demo bool) (detour.TrafficProvider, error) {
	if demo && strings.TrimSpace(apiKey) == "" {
		return missingTrafficCredentials{}, nil
	}
	return NewTomTomTrafficClient(apiKey, logger)
}

type missingTrafficCredentials struct{}

func (missingTrafficCredentials) Snapshot(context.Context, []traffic.Tile) (traffic.Snapshot, error) {
	return traffic.Snapshot{}, &traffic.Error{Code: traffic.ErrorAPIKeyMissing, Message: "TomTom is not configured"}
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

// NewSegmentService wires single-segment routing to the RF05 road graph.
func NewSegmentService(database *pgxpool.Pool) (*detour.SegmentService, error) {
	return detour.NewSegmentService(
		postgres.NewDetourRepository(database),
		config.DetourPolicy(),
		config.SegmentPolicy(),
	)
}

// NewBottlenecksService reads finalized passages without touching producer state.
func NewBottlenecksService(database *pgxpool.Pool) (*bottlenecks.DetectionService, error) {
	return bottlenecks.NewDetectionService(postgres.NewBottlenecksDetectionRepository(database), config.BottlenecksModel().Name, config.BottlenecksDetectionPolicy())
}

// NewBunchingService reads current events independently of simulations.
func NewBunchingService(database *pgxpool.Pool) (*bunching.Service, error) {
	return bunching.NewService(postgres.NewBunchingRepository(database), config.BunchingPolicy(), config.BunchingHistoryPolicy(), config.BunchingRecurrencePolicy())
}

// NewLinesService wires stored-line export to PostgreSQL.
func NewLinesService(database *pgxpool.Pool) *lines.Service {
	return lines.NewService(
		postgres.NewLinesRepository(database),
		config.LinesPolicy(),
	)
}

// NewCombinacionesService wires the reader that reports which origin-destination
// flows people cover by combining two buses.
func NewCombinacionesService(database *pgxpool.Pool) *combinaciones.Service {
	return combinaciones.NewService(
		postgres.NewCombinacionesRepository(database),
		config.CombinacionesPolicy(),
	)
}
