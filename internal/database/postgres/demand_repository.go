package postgres

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed find_cell_candidates.sql
var findCellCandidatesSQL string

//go:embed find_demand_by_stop_pair.sql
var findDemandByStopPairSQL string

// DemandRepository calculates the spatial inputs and aggregated demand for a
// route using PostgreSQL, PostGIS, and H3.
type DemandRepository struct {
	query queryFunc
}

// NewDemandRepository creates a PostgreSQL demand repository.
func NewDemandRepository(database *pgxpool.Pool) *DemandRepository {
	return newDemandRepository(func(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (rowIterator, error) {
		return database.Query(ctx, sql, arguments...)
	})
}

func newDemandRepository(query queryFunc) *DemandRepository {
	return &DemandRepository{query: query}
}

// FindCellCandidates returns the H3 cells that are close enough to each stop.
func (repository *DemandRepository) FindCellCandidates(
	ctx context.Context,
	input route.Route,
	radiusMeters float64,
) ([]demand.CellCandidate, error) {
	stopIDs := make([]string, len(input.Stops))
	longitudes := make([]float64, len(input.Stops))
	latitudes := make([]float64, len(input.Stops))
	for index, stop := range input.Stops {
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

	candidates := make([]demand.CellCandidate, 0, len(input.Stops)*7)
	for rows.Next() {
		var candidate demand.CellCandidate
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
func (repository *DemandRepository) FindDemandByStopPair(
	ctx context.Context,
	cells []demand.AssignedCell,
) ([]demand.StopPairDemand, error) {
	if len(cells) == 0 {
		return []demand.StopPairDemand{}, nil
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

	result := make([]demand.StopPairDemand, 0)
	for rows.Next() {
		var pair demand.StopPairDemand
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
		result = append(result, pair)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate demand by stop pair: %w", err)
	}
	return result, nil
}

var _ demand.Repository = (*DemandRepository)(nil)
