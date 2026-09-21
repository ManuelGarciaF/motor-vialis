package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/paulmach/orb/geojson"
)

//go:embed analyze_detour.sql
var analyzeDetourSQL string

//go:embed create_detour_graph.sql
var createDetourGraphSQL string

//go:embed snap_detour_points.sql
var snapDetourPointsSQL string

//go:embed create_detour_combinations.sql
var createDetourCombinationsSQL string

//go:embed find_detour_paths.sql
var findDetourPathsSQL string

//go:embed find_detour_topology_pairs.sql
var findDetourTopologyPairsSQL string

//go:embed detour_graph_trace.sql
var detourGraphTraceSQL string

// DetourRepository performs the spatial and routing operations for a road cut.
type DetourRepository struct {
	database *pgxpool.Pool
	query    queryFunc
}

// NewDetourRepository creates a PostgreSQL detour repository.
func NewDetourRepository(database *pgxpool.Pool) *DetourRepository {
	return &DetourRepository{
		database: database,
		query: func(ctx context.Context, query string, arguments ...any) (rowIterator, error) {
			return database.Query(ctx, query, arguments...)
		},
	}
}

func newDetourRepository(query queryFunc) *DetourRepository {
	return &DetourRepository{query: query}
}

// Analyze creates the geographic search area, finds blocked streets, and
// locates every contiguous portion of the input route inside that area.
func (repository *DetourRepository) Analyze(
	ctx context.Context,
	input route.Route,
	cut detour.Cut,
	policy detour.Policy,
) (detour.Analysis, error) {
	encodedCut, err := json.Marshal(cut.LineString)
	if err != nil {
		return detour.Analysis{}, fmt.Errorf("encode detour cut: %w", err)
	}
	segments := make([]map[string]any, 0, len(input.Stops)-1)
	for order := 0; order < len(input.Stops)-1; order++ {
		segments = append(segments, map[string]any{
			"order": order,
			"path":  input.Stops[order].PathToNext,
		})
	}
	encodedSegments, err := json.Marshal(segments)
	if err != nil {
		return detour.Analysis{}, fmt.Errorf("encode route segments: %w", err)
	}

	rows, err := repository.query(
		ctx,
		analyzeDetourSQL,
		encodedCut,
		policy.ForbiddenCorridorMeters,
		policy.SearchRadiusMeters,
		encodedSegments,
	)
	if err != nil {
		return detour.Analysis{}, fmt.Errorf("analyze detour: %w", err)
	}
	defer rows.Close()

	var (
		result          detour.Analysis
		pieces          []insidePiece
		scopeIntersects bool
		found           bool
	)
	for rows.Next() {
		found = true
		var (
			searchArea, forbiddenArea     []byte
			segmentOrder                  sql.NullInt64
			startFraction, endFraction    sql.NullFloat64
			startLongitude, startLatitude sql.NullFloat64
			startDirection                sql.NullFloat64
			endLongitude, endLatitude     sql.NullFloat64
			endDirection                  sql.NullFloat64
		)
		if err := rows.Scan(
			&result.GraphLoadID,
			&scopeIntersects,
			&result.RouteAffected,
			&searchArea,
			&forbiddenArea,
			&result.SearchBounds.MinLongitude,
			&result.SearchBounds.MinLatitude,
			&result.SearchBounds.MaxLongitude,
			&result.SearchBounds.MaxLatitude,
			&result.BlockedStreetIDs,
			&segmentOrder,
			&startFraction,
			&endFraction,
			&startLongitude,
			&startLatitude,
			&startDirection,
			&endLongitude,
			&endLatitude,
			&endDirection,
		); err != nil {
			return detour.Analysis{}, fmt.Errorf("scan detour analysis: %w", err)
		}
		result.SearchArea = append(result.SearchArea[:0], searchArea...)
		result.ForbiddenArea = append(result.ForbiddenArea[:0], forbiddenArea...)
		if !segmentOrder.Valid {
			continue
		}
		values := []sql.NullFloat64{
			startFraction,
			endFraction,
			startLongitude,
			startLatitude,
			startDirection,
			endLongitude,
			endLatitude,
			endDirection,
		}
		for _, value := range values {
			if !value.Valid {
				return detour.Analysis{}, fmt.Errorf(
					"analyze detour: segment %d has an invalid anchor",
					segmentOrder.Int64,
				)
			}
		}
		pieces = append(pieces, insidePiece{
			segmentOrder: int(segmentOrder.Int64),
			start: detour.Anchor{
				SegmentOrder: int(segmentOrder.Int64),
				Fraction:     startFraction.Float64,
				Position: route.Position{
					Longitude: startLongitude.Float64,
					Latitude:  startLatitude.Float64,
				},
				DirectionDegrees: startDirection.Float64,
			},
			end: detour.Anchor{
				SegmentOrder: int(segmentOrder.Int64),
				Fraction:     endFraction.Float64,
				Position: route.Position{
					Longitude: endLongitude.Float64,
					Latitude:  endLatitude.Float64,
				},
				DirectionDegrees: endDirection.Float64,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return detour.Analysis{}, fmt.Errorf("iterate detour analysis: %w", err)
	}
	if !found {
		return detour.Analysis{}, fmt.Errorf("analyze detour: active road graph metadata not found")
	}
	if !scopeIntersects {
		return detour.Analysis{}, &detour.Error{
			Code:    detour.ErrorCutOutsideGraph,
			Kind:    detour.ErrorKindInvalidInput,
			Message: "cut does not intersect the active road graph coverage",
		}
	}
	if len(result.BlockedStreetIDs) == 0 {
		return detour.Analysis{}, &detour.Error{
			Code:    detour.ErrorCutNotMatched,
			Kind:    detour.ErrorKindNoSolution,
			Message: "cut does not block an edge in the active road graph",
		}
	}
	if !result.RouteAffected {
		return detour.Analysis{}, &detour.Error{
			Code:    detour.ErrorRouteNotAffected,
			Kind:    detour.ErrorKindNoSolution,
			Message: "cut does not affect the input route",
		}
	}
	result.Intervals = mergeInsidePieces(pieces)
	return result, nil
}

// Route builds the traffic-priced local graph, projects the requested points,
// and finds the fastest path for every requested pair.
func (repository *DetourRepository) Route(
	ctx context.Context,
	request detour.RoutingRequest,
	policy detour.Policy,
) (detour.RoutingResult, error) {
	if repository.database == nil {
		return detour.RoutingResult{}, fmt.Errorf("route detour: database pool is nil")
	}
	if err := validateRoutingRequest(request); err != nil {
		return detour.RoutingResult{}, err
	}
	trafficJSON, err := encodeTraffic(request.Traffic)
	if err != nil {
		return detour.RoutingResult{}, err
	}
	pointsJSON, err := encodeRoutingPoints(request.Points)
	if err != nil {
		return detour.RoutingResult{}, err
	}
	pairsJSON, err := json.Marshal(request.Pairs)
	if err != nil {
		return detour.RoutingResult{}, fmt.Errorf("encode detour point pairs: %w", err)
	}

	transaction, err := repository.database.Begin(ctx)
	if err != nil {
		return detour.RoutingResult{}, fmt.Errorf("begin detour routing transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	if _, err := transaction.Exec(
		ctx,
		createDetourGraphSQL,
		pgx.QueryExecModeSimpleProtocol,
		string(request.Analysis.SearchArea),
		string(request.Analysis.ForbiddenArea),
		request.Analysis.BlockedStreetIDs,
		string(trafficJSON),
		policy.TrafficMatchRadiusMeters,
		policy.TrafficDirectionToleranceDegrees,
		policy.TrafficEstimateRadiusMeters,
		policy.TrafficEstimateMaximumSamples,
		policy.TrafficEstimateMinimumSamples,
	); err != nil {
		return detour.RoutingResult{}, fmt.Errorf("create detour traffic graph: %w", err)
	}
	if _, err := transaction.Exec(
		ctx,
		snapDetourPointsSQL,
		pgx.QueryExecModeSimpleProtocol,
		string(pointsJSON),
		policy.PointDirectionToleranceDegrees,
	); err != nil {
		return detour.RoutingResult{}, fmt.Errorf("project detour points: %w", err)
	}

	result, err := readUnmatchedPoints(ctx, transaction, request.Points)
	if err != nil {
		return detour.RoutingResult{}, err
	}
	if _, err := transaction.Exec(ctx, createDetourCombinationsSQL, pairsJSON); err != nil {
		return detour.RoutingResult{}, fmt.Errorf("create detour point combinations: %w", err)
	}
	rows, err := transaction.Query(ctx, findDetourPathsSQL)
	if err != nil {
		return detour.RoutingResult{}, fmt.Errorf("find detour paths: %w", err)
	}
	for rows.Next() {
		var path detour.GraphPath
		var encodedGeometry []byte
		if err := rows.Scan(
			&path.From,
			&path.To,
			&path.TravelSeconds,
			&path.EdgeIDs,
			&encodedGeometry,
		); err != nil {
			rows.Close()
			return detour.RoutingResult{}, fmt.Errorf("scan detour path: %w", err)
		}
		if err := json.Unmarshal(encodedGeometry, &path.Geometry); err != nil {
			rows.Close()
			return detour.RoutingResult{}, fmt.Errorf("decode detour path geometry: %w", err)
		}
		result.Paths = append(result.Paths, path)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return detour.RoutingResult{}, fmt.Errorf("iterate detour paths: %w", err)
	}
	rows.Close()

	topologyRows, err := transaction.Query(ctx, findDetourTopologyPairsSQL)
	if err != nil {
		return detour.RoutingResult{}, fmt.Errorf("diagnose detour topology paths: %w", err)
	}
	for topologyRows.Next() {
		var pair detour.PointPair
		if err := topologyRows.Scan(&pair.From, &pair.To); err != nil {
			topologyRows.Close()
			return detour.RoutingResult{}, fmt.Errorf("scan detour topology path: %w", err)
		}
		result.TopologyAvailablePairs = append(result.TopologyAvailablePairs, pair)
	}
	if err := topologyRows.Err(); err != nil {
		topologyRows.Close()
		return detour.RoutingResult{}, fmt.Errorf("iterate detour topology paths: %w", err)
	}
	topologyRows.Close()

	if err := transaction.QueryRow(ctx, detourGraphTraceSQL).Scan(
		&result.Trace.CandidateEdges,
		&result.Trace.BlockedEdges,
		&result.Trace.ForwardDirectEdges,
		&result.Trace.ReverseDirectEdges,
		&result.Trace.ForwardEstimatedEdges,
		&result.Trace.ReverseEstimatedEdges,
	); err != nil {
		return detour.RoutingResult{}, fmt.Errorf("read detour graph trace: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return detour.RoutingResult{}, fmt.Errorf("commit detour routing transaction: %w", err)
	}
	return result, nil
}

func validateRoutingRequest(request detour.RoutingRequest) error {
	if len(request.Analysis.SearchArea) == 0 {
		return fmt.Errorf("route detour: search area is empty")
	}
	if len(request.Analysis.ForbiddenArea) == 0 {
		return fmt.Errorf("route detour: forbidden area is empty")
	}
	if len(request.Traffic) == 0 {
		return &detour.Error{
			Code:    detour.ErrorTrafficUnavailable,
			Kind:    detour.ErrorKindDependency,
			Message: "traffic snapshot contains no segments",
		}
	}
	pointIDs := make(map[int64]struct{}, len(request.Points))
	for _, point := range request.Points {
		if point.ID <= 0 || point.MaximumDistanceMeters <= 0 ||
			math.IsNaN(point.DirectionDegrees) || math.IsInf(point.DirectionDegrees, 0) ||
			math.IsNaN(point.Position.Latitude) || math.IsInf(point.Position.Latitude, 0) ||
			math.IsNaN(point.Position.Longitude) || math.IsInf(point.Position.Longitude, 0) ||
			point.Position.Latitude < -90 || point.Position.Latitude > 90 ||
			point.Position.Longitude < -180 || point.Position.Longitude > 180 {
			return fmt.Errorf("route detour: invalid routing point %d", point.ID)
		}
		switch point.Role {
		case detour.PointAnchor, detour.PointRequiredStop, detour.PointOptionalStop:
		default:
			return fmt.Errorf("route detour: routing point %d has invalid role %q", point.ID, point.Role)
		}
		if _, exists := pointIDs[point.ID]; exists {
			return fmt.Errorf("route detour: duplicate routing point %d", point.ID)
		}
		pointIDs[point.ID] = struct{}{}
	}
	for _, pair := range request.Pairs {
		if _, exists := pointIDs[pair.From]; !exists {
			return fmt.Errorf("route detour: pair references unknown point %d", pair.From)
		}
		if _, exists := pointIDs[pair.To]; !exists {
			return fmt.Errorf("route detour: pair references unknown point %d", pair.To)
		}
	}
	return nil
}

func encodeTraffic(segments []traffic.Segment) ([]byte, error) {
	collection := geojson.NewFeatureCollection()
	for _, segment := range segments {
		feature := geojson.NewFeature(segment.Geometry)
		feature.Properties = geojson.Properties{
			"source_id":     segment.SourceID,
			"road_category": segment.RoadCategory,
			"road_coverage": segment.RoadCoverage,
			"speed_kph":     segment.SpeedKPH,
			"has_speed":     segment.HasSpeed,
			"closure":       segment.Closure,
		}
		collection.Append(feature)
	}
	encoded, err := json.Marshal(collection)
	if err != nil {
		return nil, fmt.Errorf("encode detour traffic: %w", err)
	}
	return encoded, nil
}

func encodeRoutingPoints(points []detour.RoutingPoint) ([]byte, error) {
	values := make([]map[string]any, len(points))
	for index, point := range points {
		values[index] = map[string]any{
			"id":                      point.ID,
			"order":                   index,
			"longitude":               point.Position.Longitude,
			"latitude":                point.Position.Latitude,
			"direction_degrees":       point.DirectionDegrees,
			"maximum_distance_meters": point.MaximumDistanceMeters,
			"role":                    point.Role,
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode detour routing points: %w", err)
	}
	return encoded, nil
}

func readUnmatchedPoints(
	ctx context.Context,
	transaction pgx.Tx,
	points []detour.RoutingPoint,
) (detour.RoutingResult, error) {
	rows, err := transaction.Query(ctx, `
SELECT requested.id
FROM detour_requested_points requested
LEFT JOIN detour_points snapped ON snapped.pid = requested.id
WHERE snapped.pid IS NULL
ORDER BY requested.route_order
`)
	if err != nil {
		return detour.RoutingResult{}, fmt.Errorf("find unmatched detour points: %w", err)
	}
	defer rows.Close()
	byID := make(map[int64]detour.RoutingPoint, len(points))
	for _, point := range points {
		byID[point.ID] = point
	}
	var result detour.RoutingResult
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return detour.RoutingResult{}, fmt.Errorf("scan unmatched detour point: %w", err)
		}
		point := byID[id]
		switch point.Role {
		case detour.PointAnchor:
			return detour.RoutingResult{}, &detour.Error{
				Code:    detour.ErrorAnchorNotMatched,
				Kind:    detour.ErrorKindNoSolution,
				Message: fmt.Sprintf("anchor point %d could not be matched", id),
			}
		case detour.PointRequiredStop:
			return detour.RoutingResult{}, &detour.Error{
				Code:    detour.ErrorRequiredStopNotMatched,
				Kind:    detour.ErrorKindNoSolution,
				Message: fmt.Sprintf("required stop point %d could not be matched", id),
			}
		default:
			result.UnmatchedPointIDs = append(result.UnmatchedPointIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		return detour.RoutingResult{}, fmt.Errorf("iterate unmatched detour points: %w", err)
	}
	return result, nil
}

type insidePiece struct {
	segmentOrder int
	start        detour.Anchor
	end          detour.Anchor
}

func mergeInsidePieces(pieces []insidePiece) []detour.AffectedInterval {
	if len(pieces) == 0 {
		return nil
	}
	result := []detour.AffectedInterval{{
		Order: 0,
		Entry: pieces[0].start,
		Exit:  pieces[0].end,
	}}
	previous := pieces[0]
	for _, piece := range pieces[1:] {
		current := &result[len(result)-1]
		if contiguousPieces(previous, piece) {
			current.Exit = piece.end
		} else {
			result = append(result, detour.AffectedInterval{
				Order: len(result),
				Entry: piece.start,
				Exit:  piece.end,
			})
		}
		previous = piece
	}
	return result
}

var _ detour.Repository = (*DetourRepository)(nil)

func contiguousPieces(left, right insidePiece) bool {
	const tolerance = 1e-9
	if left.segmentOrder == right.segmentOrder {
		return math.Abs(left.end.Fraction-right.start.Fraction) <= tolerance
	}
	return right.segmentOrder == left.segmentOrder+1 &&
		left.end.Fraction >= 1-tolerance &&
		right.start.Fraction <= tolerance
}
