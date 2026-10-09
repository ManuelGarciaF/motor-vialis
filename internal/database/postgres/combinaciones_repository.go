package postgres

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed find_frequent_transfers.sql
var findFrequentTransfersSQL string

var _ combinaciones.Repository = (*CombinacionesRepository)(nil)

// CombinacionesRepository reads the precomputed ranking of line combinations.
type CombinacionesRepository struct {
	query queryFunc
}

// NewCombinacionesRepository creates a PostgreSQL combination repository.
func NewCombinacionesRepository(database *pgxpool.Pool) *CombinacionesRepository {
	return newCombinacionesRepository(func(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (rowIterator, error) {
		return database.Query(ctx, sql, arguments...)
	})
}

func newCombinacionesRepository(query queryFunc) *CombinacionesRepository {
	return &CombinacionesRepository{query: query}
}

// FindRanking returns one window over the ranking, its total and the largest
// estimate in the whole ranking.
//
// The total and the maximum are window functions repeated on every row, so
// they are read from whichever row arrives and not accumulated. An empty
// window legitimately reports zero for both: a page past the end of the
// ranking has no rows to read them from, and a caller asking for offset 900 of
// a 10-row ranking is asking about nothing.
func (repository *CombinacionesRepository) FindRanking(
	ctx context.Context,
	query combinaciones.Query,
) ([]combinaciones.Combination, int, float64, error) {
	rows, err := repository.query(
		ctx,
		findFrequentTransfersSQL,
		query.Hour,
		query.Limit,
		query.Offset,
	)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("query combination ranking: %w", err)
	}
	defer rows.Close()

	var (
		combinations []combinaciones.Combination
		total        int
		maximum      float64
	)
	for rows.Next() {
		var (
			combination combinaciones.Combination
			rowTotal    int
			rowMaximum  float64
		)
		if err := rows.Scan(
			&rowTotal,
			&rowMaximum,

			&combination.First.LineID,
			&combination.First.Line,
			&combination.First.Branch,
			&combination.First.PublicName,
			&combination.First.DirectionID,

			&combination.Second.LineID,
			&combination.Second.Line,
			&combination.Second.Branch,
			&combination.Second.PublicName,
			&combination.Second.DirectionID,

			&combination.EstimatedTrips,
			&combination.AverageAlternatives,
			&combination.PeakHour,

			&combination.Transfer.AlightingStopName,
			&combination.Transfer.BoardingStopName,
			&combination.Transfer.WalkMeters,
			&combination.Transfer.Longitude,
			&combination.Transfer.Latitude,

			&combination.DistinctFlows,

			&combination.Origin.H3Index,
			&combination.Origin.Name,
			&combination.Origin.Longitude,
			&combination.Origin.Latitude,

			&combination.Destination.H3Index,
			&combination.Destination.Name,
			&combination.Destination.Longitude,
			&combination.Destination.Latitude,
		); err != nil {
			return nil, 0, 0, fmt.Errorf("scan combination: %w", err)
		}

		total = rowTotal
		maximum = rowMaximum
		combinations = append(combinations, combination)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("read combination ranking: %w", err)
	}
	return combinations, total, maximum, nil
}
