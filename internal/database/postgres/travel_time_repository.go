package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed find_segment_references.sql
var findSegmentReferencesSQL string

//go:embed find_global_paces.sql
var findGlobalPacesSQL string

// TravelTimeRepository measures input segments and reads GTFS commercial-time
// references using PostGIS.
type TravelTimeRepository struct {
	query queryFunc
}

// NewTravelTimeRepository creates a PostgreSQL travel-time repository.
func NewTravelTimeRepository(database *pgxpool.Pool) *TravelTimeRepository {
	return newTravelTimeRepository(func(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (rowIterator, error) {
		return database.Query(ctx, sql, arguments...)
	})
}

func newTravelTimeRepository(query queryFunc) *TravelTimeRepository {
	return &TravelTimeRepository{query: query}
}

// FindSegmentReferences measures all input LineStrings and returns references
// for every configured corridor radius.
func (repository *TravelTimeRepository) FindSegmentReferences(
	ctx context.Context,
	segments []traveltime.Segment,
	policy traveltime.Policy,
) ([]traveltime.MeasuredSegment, error) {
	orders := make([]int32, len(segments))
	originStopIDs := make([]string, len(segments))
	destinationStopIDs := make([]string, len(segments))
	paths := make([]string, len(segments))
	for index, segment := range segments {
		orders[index] = int32(segment.Order)
		originStopIDs[index] = segment.OriginStopID
		destinationStopIDs[index] = segment.DestinationStopID
		encodedPath, err := json.Marshal(segment.Path)
		if err != nil {
			return nil, fmt.Errorf(
				"encode segment %d LineString: %w",
				segment.Order,
				err,
			)
		}
		paths[index] = string(encodedPath)
	}

	rows, err := repository.query(
		ctx,
		findSegmentReferencesSQL,
		orders,
		originStopIDs,
		destinationStopIDs,
		paths,
		policy.ReferenceRadiiMeters,
		policy.DirectionToleranceDegrees,
		policy.MinimumCommercialSpeedKPH,
		policy.MaximumCommercialSpeedKPH,
	)
	if err != nil {
		return nil, fmt.Errorf("query segment references: %w", err)
	}
	defer rows.Close()

	byOrder := make(map[int]*traveltime.MeasuredSegment, len(segments))
	for rows.Next() {
		var (
			order             int
			originStopID      string
			destinationStopID string
			lengthMeters      float64
			radiusMeters      float64
			routeID           sql.NullInt64
			distanceMeters    sql.NullFloat64
			overlapMeters     sql.NullFloat64
			offPeakPace       sql.NullFloat64
			typicalPace       sql.NullFloat64
			peakPace          sql.NullFloat64
		)
		if err := rows.Scan(
			&order,
			&originStopID,
			&destinationStopID,
			&lengthMeters,
			&radiusMeters,
			&routeID,
			&distanceMeters,
			&overlapMeters,
			&offPeakPace,
			&typicalPace,
			&peakPace,
		); err != nil {
			return nil, fmt.Errorf("scan segment reference: %w", err)
		}

		measured, exists := byOrder[order]
		if !exists {
			measured = &traveltime.MeasuredSegment{
				Segment: traveltime.Segment{
					Order:             order,
					OriginStopID:      originStopID,
					DestinationStopID: destinationStopID,
				},
				LengthMeters: lengthMeters,
			}
			byOrder[order] = measured
		}
		if routeID.Valid &&
			distanceMeters.Valid &&
			overlapMeters.Valid &&
			offPeakPace.Valid &&
			typicalPace.Valid &&
			peakPace.Valid {
			measured.References = append(
				measured.References,
				traveltime.Reference{
					RouteID:        routeID.Int64,
					RadiusMeters:   radiusMeters,
					DistanceMeters: distanceMeters.Float64,
					OverlapMeters:  overlapMeters.Float64,
					Paces: traveltime.Paces{
						OffPeak: offPeakPace.Float64,
						Typical: typicalPace.Float64,
						Peak:    peakPace.Float64,
					},
				},
			)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate segment references: %w", err)
	}

	result := make([]traveltime.MeasuredSegment, 0, len(byOrder))
	for _, segment := range byOrder {
		result = append(result, *segment)
	}
	return result, nil
}

// FindGlobalPaces returns one median commercial pace per scenario, giving each
// existing route one observation.
func (repository *TravelTimeRepository) FindGlobalPaces(
	ctx context.Context,
	policy traveltime.Policy,
) (traveltime.GlobalPaces, error) {
	rows, err := repository.query(
		ctx,
		findGlobalPacesSQL,
		policy.MinimumCommercialSpeedKPH,
		policy.MaximumCommercialSpeedKPH,
	)
	if err != nil {
		return traveltime.GlobalPaces{}, fmt.Errorf(
			"query global commercial paces: %w",
			err,
		)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return traveltime.GlobalPaces{}, fmt.Errorf(
				"iterate global commercial paces: %w",
				err,
			)
		}
		return traveltime.GlobalPaces{}, nil
	}

	var (
		offPeakPace sql.NullFloat64
		typicalPace sql.NullFloat64
		peakPace    sql.NullFloat64
		routeCount  int
	)
	if err := rows.Scan(
		&offPeakPace,
		&typicalPace,
		&peakPace,
		&routeCount,
	); err != nil {
		return traveltime.GlobalPaces{}, fmt.Errorf(
			"scan global commercial paces: %w",
			err,
		)
	}
	if err := rows.Err(); err != nil {
		return traveltime.GlobalPaces{}, fmt.Errorf(
			"iterate global commercial paces: %w",
			err,
		)
	}
	if !offPeakPace.Valid || !typicalPace.Valid || !peakPace.Valid {
		return traveltime.GlobalPaces{RouteCount: routeCount}, nil
	}
	return traveltime.GlobalPaces{
		Paces: traveltime.Paces{
			OffPeak: offPeakPace.Float64,
			Typical: typicalPace.Float64,
			Peak:    peakPace.Float64,
		},
		RouteCount: routeCount,
	}, nil
}

var _ traveltime.Repository = (*TravelTimeRepository)(nil)
