// Package app is the shared composition root for command binaries.
package app

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
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

// NewLinesService wires stored-line export to PostgreSQL.
func NewLinesService(database *pgxpool.Pool) *lines.Service {
	return lines.NewService(
		postgres.NewLinesRepository(database),
		config.LinesPolicy(),
	)
}
