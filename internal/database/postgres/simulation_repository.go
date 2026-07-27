package postgres

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed find_cell_candidates.sql
var findCellCandidatesSQL string

//go:embed find_demand_by_stop_pair.sql
var findDemandByStopPairSQL string

type rowIterator interface {
	Next() bool
	Scan(destinations ...any) error
	Err() error
	Close()
}

type queryFunc func(ctx context.Context, sql string, arguments ...any) (rowIterator, error)

// SimulationRepository calculates the spatial inputs and aggregated demand for
// a route using PostgreSQL, PostGIS, and H3.
type SimulationRepository struct {
	query queryFunc
}

// NewSimulationRepository creates a PostgreSQL simulation repository.
func NewSimulationRepository(database *pgxpool.Pool) *SimulationRepository {
	return newSimulationRepository(func(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (rowIterator, error) {
		return database.Query(ctx, sql, arguments...)
	})
}

func newSimulationRepository(query queryFunc) *SimulationRepository {
	return &SimulationRepository{query: query}
}

// FindCellCandidates returns the H3 cells that are close enough to each stop.
func (repository *SimulationRepository) FindCellCandidates(
	ctx context.Context,
	route simulation.Route,
	radiusMeters float64,
) ([]simulation.CellCandidate, error) {
	stopIDs := make([]string, len(route.Stops))
	longitudes := make([]float64, len(route.Stops))
	latitudes := make([]float64, len(route.Stops))
	for index, stop := range route.Stops {
		stopIDs[index] = stop.ID
		longitudes[index] = stop.Position.Longitude
		latitudes[index] = stop.Position.Latitude
	}

	rows, err := repository.query(
		ctx,
		findCellCandidatesSQL,
		stopIDs,
		longitudes,
		latitudes,
		radiusMeters,
	)
	if err != nil {
		return nil, fmt.Errorf("query cell candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]simulation.CellCandidate, 0, len(route.Stops)*7)
	for rows.Next() {
		var candidate simulation.CellCandidate
		if err := rows.Scan(
			&candidate.StopOrder,
			&candidate.StopID,
			&candidate.CellID,
			&candidate.DistanceMeters,
		); err != nil {
			return nil, fmt.Errorf("scan cell candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cell candidates: %w", err)
	}
	return candidates, nil
}

// FindDemandByStopPair aggregates the OD matrix for all downstream stop pairs.
func (repository *SimulationRepository) FindDemandByStopPair(
	ctx context.Context,
	cells []simulation.AssignedCell,
) ([]simulation.StopPairDemand, error) {
	if len(cells) == 0 {
		return []simulation.StopPairDemand{}, nil
	}

	stopOrders := make([]int32, len(cells))
	stopIDs := make([]string, len(cells))
	cellIDs := make([]string, len(cells))
	accessibilities := make([]float64, len(cells))
	for index, cell := range cells {
		stopOrders[index] = int32(cell.StopOrder)
		stopIDs[index] = cell.StopID
		cellIDs[index] = string(cell.CellID)
		accessibilities[index] = cell.Accessibility
	}

	rows, err := repository.query(
		ctx,
		findDemandByStopPairSQL,
		stopOrders,
		stopIDs,
		cellIDs,
		accessibilities,
	)
	if err != nil {
		return nil, fmt.Errorf("query demand by stop pair: %w", err)
	}
	defer rows.Close()

	demand := make([]simulation.StopPairDemand, 0)
	for rows.Next() {
		var pair simulation.StopPairDemand
		if err := rows.Scan(
			&pair.OriginStopOrder,
			&pair.OriginStopID,
			&pair.DestinationStopOrder,
			&pair.DestinationStopID,
			&pair.GrossDemand,
			&pair.PotentialDemand,
		); err != nil {
			return nil, fmt.Errorf("scan demand by stop pair: %w", err)
		}
		demand = append(demand, pair)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate demand by stop pair: %w", err)
	}
	return demand, nil
}

var _ rowIterator = pgx.Rows(nil)
var _ simulation.Repository = (*SimulationRepository)(nil)
