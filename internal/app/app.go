// Package app is the composition root shared by the binaries in cmd/. It is the
// single place where the model parameters in internal/config meet the
// PostgreSQL repositories, so cmd/api and cmd/simulation-test cannot drift into
// simulating with different assumptions.
package app

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// NewSimulationService wires the three estimators against database and returns
// the orchestrator that runs them in order.
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

// NewLinesService wires the reader that exports stored GTFS lines as routes a
// caller can modify and resubmit.
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
