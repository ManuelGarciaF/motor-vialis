package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
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

// storedFlow mirrors the JSON object the query builds for each top flow. The
// three of them travel as one JSON column rather than as repeated rows so the
// twenty columns describing the combination are not multiplied by three to
// carry nine values.
type storedFlow struct {
	H3Origin        string  `json:"h3Origen"`
	LongitudeOrigin float64 `json:"lonOrigen"`
	LatitudeOrigin  float64 `json:"latOrigen"`
	H3Destination   string  `json:"h3Destino"`
	LongitudeDest   float64 `json:"lonDestino"`
	LatitudeDest    float64 `json:"latDestino"`
	Hour            int     `json:"hora"`
	EstimatedTrips  float64 `json:"viajes"`
	Alternatives    int     `json:"alternativas"`
	OriginName      string  `json:"nombreOrigen"`
	DestinationName string  `json:"nombreDestino"`
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
			flowsJSON   []byte
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

			&flowsJSON,
		); err != nil {
			return nil, 0, 0, fmt.Errorf("scan combination: %w", err)
		}

		flows, err := decodeFlows(flowsJSON)
		if err != nil {
			return nil, 0, 0, err
		}
		combination.TopFlows = flows

		total = rowTotal
		maximum = rowMaximum
		combinations = append(combinations, combination)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, fmt.Errorf("read combination ranking: %w", err)
	}
	return combinations, total, maximum, nil
}

func decodeFlows(raw []byte) ([]combinaciones.Flow, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var stored []storedFlow
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("decode top flows: %w", err)
	}
	flows := make([]combinaciones.Flow, len(stored))
	for index, flow := range stored {
		flows[index] = combinaciones.Flow{
			Origin: combinaciones.Cell{
				H3Index:   flow.H3Origin,
				Name:      flow.OriginName,
				Longitude: flow.LongitudeOrigin,
				Latitude:  flow.LatitudeOrigin,
			},
			Destination: combinaciones.Cell{
				H3Index:   flow.H3Destination,
				Name:      flow.DestinationName,
				Longitude: flow.LongitudeDest,
				Latitude:  flow.LatitudeDest,
			},
			Hour:           flow.Hour,
			EstimatedTrips: flow.EstimatedTrips,
			Alternatives:   flow.Alternatives,
		}
	}
	return flows, nil
}
