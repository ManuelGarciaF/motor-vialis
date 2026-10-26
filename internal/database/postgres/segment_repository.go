package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/jackc/pgx/v5"
)

//go:embed prepare_segment_route.sql
var prepareSegmentRouteSQL string

// emptyTraffic makes every edge fall back to the policy default speed, so a
// segment is routed by length without a traffic provider call per edit.
const emptyTraffic = `{"type":"FeatureCollection","features":[]}`

// RouteSegment routes each consecutive waypoint pair over the RF05 local graph:
// the same cut blocking, snapping, one-way costs, turn restrictions and U-turn
// filters as Route, but bounded by the waypoints instead of the cut and priced
// by length instead of live traffic.
func (repository *DetourRepository) RouteSegment(
	ctx context.Context,
	request detour.SegmentRoutingRequest,
	policy detour.Policy,
) (detour.SegmentRoute, error) {
	if repository.database == nil {
		return detour.SegmentRoute{}, fmt.Errorf("route segment: database pool is nil")
	}
	if len(request.Points) < 2 {
		return detour.SegmentRoute{}, fmt.Errorf("route segment: at least two points are required")
	}

	waypoints := route.LineString{Positions: make([]route.Position, len(request.Points))}
	for index, point := range request.Points {
		waypoints.Positions[index] = point.Position
	}
	encodedWaypoints, err := json.Marshal(waypoints)
	if err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("encode segment waypoints: %w", err)
	}
	var encodedCut []byte
	if request.Avoid != nil {
		encodedCut, err = json.Marshal(route.MultiLineString{Lines: request.Avoid.Lines})
		if err != nil {
			return detour.SegmentRoute{}, fmt.Errorf("encode segment cut: %w", err)
		}
	}
	pointsJSON, err := encodeRoutingPoints(request.Points)
	if err != nil {
		return detour.SegmentRoute{}, err
	}
	pairs := make([]detour.PointPair, 0, len(request.Points)-1)
	for index := 0; index < len(request.Points)-1; index++ {
		pairs = append(pairs, detour.PointPair{From: request.Points[index].ID, To: request.Points[index+1].ID})
	}
	pairsJSON, err := json.Marshal(pairs)
	if err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("encode segment pairs: %w", err)
	}

	transaction, err := repository.database.Begin(ctx)
	if err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("begin segment routing transaction: %w", err)
	}
	// Every table is temporary and the request writes nothing, so the
	// transaction is always rolled back.
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := disableJIT(ctx, transaction); err != nil {
		return detour.SegmentRoute{}, err
	}

	var (
		searchArea, forbiddenArea []byte
		blockedStreetIDs          []int64
	)
	var cutArgument any
	if encodedCut != nil {
		cutArgument = string(encodedCut)
	}
	// The waypoints travel as a LineString: its envelope is the bounding box.
	if err := transaction.QueryRow(
		ctx,
		prepareSegmentRouteSQL,
		string(encodedWaypoints),
		cutArgument,
		policy.ForbiddenCorridorMeters,
		request.MarginMeters,
	).Scan(&searchArea, &forbiddenArea, &blockedStreetIDs); err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("prepare segment graph: %w", err)
	}

	if _, err := transaction.Exec(
		ctx,
		createDetourGraphSQL,
		pgx.QueryExecModeSimpleProtocol,
		string(searchArea),
		string(forbiddenArea),
		blockedStreetIDs,
		emptyTraffic,
		policy.TrafficMatchRadiusMeters,
		policy.TrafficDirectionToleranceDegrees,
		policy.TrafficEstimateRadiusMeters,
		policy.TrafficEstimateMaximumSamples,
		policy.TrafficEstimateMinimumSamples,
		policy.DefaultSpeedKPH,
		0.0,
	); err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("create segment graph: %w", err)
	}
	if _, err := transaction.Exec(
		ctx,
		snapDetourPointsSQL,
		pgx.QueryExecModeSimpleProtocol,
		string(pointsJSON),
		policy.PointDirectionToleranceDegrees,
	); err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("project segment waypoints: %w", err)
	}
	if err := checkSegmentWaypointsMatched(ctx, transaction); err != nil {
		return detour.SegmentRoute{}, err
	}
	if _, err := transaction.Exec(ctx, createDetourCombinationsSQL, pairsJSON); err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("create segment pairs: %w", err)
	}

	byPair := make(map[detour.PointPair]route.LineString, len(pairs))
	if err := collectSegmentPaths(ctx, transaction, byPair, findDetourPathsSQL); err != nil {
		return detour.SegmentRoute{}, err
	}
	// Like Route, a pair dropped only for a spurious U-turn is re-routed alone.
	retryPairs, err := readUturnPairs(ctx, transaction)
	if err != nil {
		return detour.SegmentRoute{}, err
	}
	if len(retryPairs) > 0 {
		retryJSON, err := json.Marshal(retryPairs)
		if err != nil {
			return detour.SegmentRoute{}, fmt.Errorf("encode segment retry pairs: %w", err)
		}
		if err := collectSegmentPaths(ctx, transaction, byPair, findDetourPathsRetrySQL, string(retryJSON)); err != nil {
			return detour.SegmentRoute{}, err
		}
	}

	paths := make([]route.LineString, 0, len(pairs))
	for index, pair := range pairs {
		path, found := byPair[pair]
		if !found {
			return detour.SegmentRoute{}, &detour.Error{
				Code: detour.ErrorNoSegmentRoute,
				Kind: detour.ErrorKindNoSolution,
				Message: fmt.Sprintf(
					"no street path joins waypoints %d and %d within %.0f m of them without crossing the cut",
					index, index+1, request.MarginMeters,
				),
			}
		}
		paths = append(paths, path)
	}
	return detour.JoinSegmentPaths(paths), nil
}

func checkSegmentWaypointsMatched(ctx context.Context, transaction pgx.Tx) error {
	var unmatched []int64
	rows, err := transaction.Query(ctx, findUnmatchedDetourPointsSQL)
	if err != nil {
		return fmt.Errorf("find unmatched segment waypoints: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("scan unmatched segment waypoint: %w", err)
		}
		unmatched = append(unmatched, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate unmatched segment waypoints: %w", err)
	}
	if len(unmatched) == 0 {
		return nil
	}
	// Point IDs are waypoint index + 1.
	return &detour.Error{
		Code:  detour.ErrorWaypointNotMatched,
		Kind:  detour.ErrorKindNoSolution,
		Field: fmt.Sprintf("waypoints[%d]", unmatched[0]-1),
		Message: fmt.Sprintf(
			"waypoint %d is not next to a usable street, or lies on the cut",
			unmatched[0]-1,
		),
	}
}

func collectSegmentPaths(
	ctx context.Context,
	transaction pgx.Tx,
	byPair map[detour.PointPair]route.LineString,
	query string,
	arguments ...any,
) error {
	rows, err := transaction.Query(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("find segment paths: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			pair            detour.PointPair
			travelSeconds   float64
			edgeIDs         []int64
			encodedGeometry []byte
			geometry        route.LineString
		)
		if err := rows.Scan(&pair.From, &pair.To, &travelSeconds, &edgeIDs, &encodedGeometry); err != nil {
			return fmt.Errorf("scan segment path: %w", err)
		}
		if err := json.Unmarshal(encodedGeometry, &geometry); err != nil {
			return fmt.Errorf("decode segment path geometry: %w", err)
		}
		byPair[pair] = geometry
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate segment paths: %w", err)
	}
	return nil
}

var _ detour.SegmentRouter = (*DetourRepository)(nil)
