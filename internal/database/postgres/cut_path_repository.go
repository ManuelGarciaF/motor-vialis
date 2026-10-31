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

//go:embed create_cut_path_graph.sql
var createCutPathGraphSQL string

//go:embed find_unmatched_cut_path_points.sql
var findUnmatchedCutPathPointsSQL string

//go:embed find_cut_path_legs.sql
var findCutPathLegsSQL string

// cutPathWaypoint is one waypoint as create_cut_path_graph.sql reads it. The
// id is the waypoint index + 1: pgRouting reserves negative ids for points.
type cutPathWaypoint struct {
	ID        int64   `json:"id"`
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

// RouteCutPath routes each consecutive waypoint pair over an undirected local
// graph: unlike RouteSegment, one-way streets can be followed against their
// direction and turn restrictions do not apply, because a cut closes streets
// rather than driving them.
func (repository *DetourRepository) RouteCutPath(
	ctx context.Context,
	request detour.CutPathRequest,
) (detour.SegmentRoute, error) {
	if repository.database == nil {
		return detour.SegmentRoute{}, fmt.Errorf("route cut path: database pool is nil")
	}
	if len(request.Waypoints) < 2 {
		return detour.SegmentRoute{}, fmt.Errorf("route cut path: at least two waypoints are required")
	}

	waypoints := make([]cutPathWaypoint, len(request.Waypoints))
	for index, position := range request.Waypoints {
		waypoints[index] = cutPathWaypoint{
			ID:        int64(index + 1),
			Longitude: position.Longitude,
			Latitude:  position.Latitude,
		}
	}
	encodedWaypoints, err := json.Marshal(waypoints)
	if err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("encode cut path waypoints: %w", err)
	}

	transaction, err := repository.database.Begin(ctx)
	if err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("begin cut path transaction: %w", err)
	}
	// Every table is temporary and the request writes nothing, so the
	// transaction is always rolled back.
	defer func() { _ = transaction.Rollback(ctx) }()

	if err := disableJIT(ctx, transaction); err != nil {
		return detour.SegmentRoute{}, err
	}
	if _, err := transaction.Exec(
		ctx,
		createCutPathGraphSQL,
		pgx.QueryExecModeSimpleProtocol,
		string(encodedWaypoints),
		request.MarginMeters,
		request.SnapDistanceMeters,
	); err != nil {
		return detour.SegmentRoute{}, fmt.Errorf("create cut path graph: %w", err)
	}
	if err := checkCutPathWaypointsMatched(ctx, transaction, request.SnapDistanceMeters); err != nil {
		return detour.SegmentRoute{}, err
	}

	legs, err := readCutPathLegs(ctx, transaction)
	if err != nil {
		return detour.SegmentRoute{}, err
	}
	paths := make([]route.LineString, 0, len(request.Waypoints)-1)
	for index := 0; index < len(request.Waypoints)-1; index++ {
		path, found := legs[int64(index+1)]
		if !found {
			return detour.SegmentRoute{}, &detour.Error{
				Code: detour.ErrorNoCutPath,
				Kind: detour.ErrorKindNoSolution,
				Message: fmt.Sprintf(
					"no street path joins waypoints %d and %d within %.0f m of them",
					index, index+1, request.MarginMeters,
				),
			}
		}
		paths = append(paths, path)
	}
	joined := detour.JoinSegmentPaths(paths)
	if len(joined.Geometry.Positions) < 2 {
		// Every waypoint snapped onto the same spot of one street.
		return detour.SegmentRoute{}, &detour.Error{
			Code:    detour.ErrorNoCutPath,
			Kind:    detour.ErrorKindNoSolution,
			Field:   "waypoints",
			Message: "all waypoints snap onto the same point of a street",
		}
	}
	return joined, nil
}

func checkCutPathWaypointsMatched(ctx context.Context, transaction pgx.Tx, snapDistanceMeters float64) error {
	rows, err := transaction.Query(ctx, findUnmatchedCutPathPointsSQL)
	if err != nil {
		return fmt.Errorf("find unmatched cut path waypoints: %w", err)
	}
	unmatched, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return fmt.Errorf("read unmatched cut path waypoints: %w", err)
	}
	if len(unmatched) == 0 {
		return nil
	}
	// Point IDs are waypoint index + 1.
	index := unmatched[0] - 1
	return &detour.Error{
		Code:    detour.ErrorWaypointNotMatched,
		Kind:    detour.ErrorKindNoSolution,
		Field:   fmt.Sprintf("waypoints[%d]", index),
		Message: fmt.Sprintf("waypoint %d is more than %.0f m from any street", index, snapDistanceMeters),
	}
}

// readCutPathLegs returns each routed leg keyed by the id of its origin
// waypoint.
func readCutPathLegs(ctx context.Context, transaction pgx.Tx) (map[int64]route.LineString, error) {
	rows, err := transaction.Query(ctx, findCutPathLegsSQL)
	if err != nil {
		return nil, fmt.Errorf("find cut path legs: %w", err)
	}
	defer rows.Close()

	legs := make(map[int64]route.LineString)
	for rows.Next() {
		var (
			from, to        int64
			encodedGeometry []byte
			geometry        route.LineString
		)
		if err := rows.Scan(&from, &to, &encodedGeometry); err != nil {
			return nil, fmt.Errorf("scan cut path leg: %w", err)
		}
		if err := json.Unmarshal(encodedGeometry, &geometry); err != nil {
			return nil, fmt.Errorf("decode cut path leg geometry: %w", err)
		}
		legs[from] = geometry
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cut path legs: %w", err)
	}
	return legs, nil
}

var _ detour.CutPathRouter = (*DetourRepository)(nil)
