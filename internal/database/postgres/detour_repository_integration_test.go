package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/paulmach/orb"
)

func TestDetourRepositoryAnalyzeIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	var (
		streetID int64
		pathJSON []byte
		cutJSON  []byte
	)
	err = pool.QueryRow(ctx, `
SELECT
    id_calle,
    ST_AsGeoJSON(geom)::bytea,
    ST_AsGeoJSON(ST_MakeLine(
        ST_Project(
            ST_LineInterpolatePoint(geom, 0.5)::geography,
            25,
            ST_Azimuth(ST_StartPoint(geom), ST_EndPoint(geom)) + pi() / 2
        )::geometry,
        ST_Project(
            ST_LineInterpolatePoint(geom, 0.5)::geography,
            25,
            ST_Azimuth(ST_StartPoint(geom), ST_EndPoint(geom)) - pi() / 2
        )::geometry
    ))::bytea
FROM vialis.calles
WHERE costo BETWEEN 200 AND 500
  AND ST_NPoints(geom) >= 2
ORDER BY id_calle
LIMIT 1
`).Scan(&streetID, &pathJSON, &cutJSON)
	if err != nil {
		t.Fatalf("prepare detour fixture: %v", err)
	}
	var path route.LineString
	if err := json.Unmarshal(pathJSON, &path); err != nil {
		t.Fatalf("decode path: %v", err)
	}
	var cut route.LineString
	if err := json.Unmarshal(cutJSON, &cut); err != nil {
		t.Fatalf("decode cut: %v", err)
	}
	input := route.Route{
		Jurisdiction: route.JurisdictionCABA,
		Stops: []route.Stop{
			{ID: "A", Position: path.Positions[0], PathToNext: &path},
			{ID: "B", Position: path.Positions[len(path.Positions)-1]},
		},
	}
	if err := route.Validate(input); err != nil {
		t.Fatalf("fixture route is invalid: %v", err)
	}

	repository := NewDetourRepository(pool)
	analysis, err := repository.Analyze(
		ctx,
		input,
		detour.Cut{LineString: cut},
		config.DetourPolicy(),
	)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if analysis.GraphLoadID <= 0 || len(analysis.SearchArea) == 0 {
		t.Fatalf("analysis metadata is incomplete: %#v", analysis)
	}
	if !analysis.RouteAffected {
		t.Fatal("route was not marked as affected")
	}
	if !slices.Contains(analysis.BlockedStreetIDs, streetID) {
		t.Fatalf("blocked streets do not contain fixture edge %d", streetID)
	}
	if len(analysis.Intervals) != 1 {
		t.Fatalf("intervals = %d, want 1: %#v", len(analysis.Intervals), analysis.Intervals)
	}
	interval := analysis.Intervals[0]
	if interval.Entry.Fraction > 0.001 || interval.Exit.Fraction < 0.999 {
		t.Fatalf("interval does not cover the route within the 1 km area: %#v", interval)
	}
}

func TestDetourRepositoryRouteIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	var (
		streetID                      int64
		pathJSON, searchArea          []byte
		startLongitude, startLatitude float64
		endLongitude, endLatitude     float64
		directionDegrees              float64
	)
	err = pool.QueryRow(ctx, `
SELECT
    id_calle,
    ST_AsGeoJSON(geom)::bytea,
    ST_AsGeoJSON(ST_Buffer(geom::geography, 5)::geometry)::bytea,
    ST_X((ST_Project(
        ST_LineInterpolatePoint(geom, 0.1)::geography,
        3,
        ST_Azimuth(ST_StartPoint(geom), ST_EndPoint(geom)) + pi() / 2
    ))::geometry),
    ST_Y((ST_Project(
        ST_LineInterpolatePoint(geom, 0.1)::geography,
        3,
        ST_Azimuth(ST_StartPoint(geom), ST_EndPoint(geom)) + pi() / 2
    ))::geometry),
    ST_X(ST_LineInterpolatePoint(geom, 0.9)),
    ST_Y(ST_LineInterpolatePoint(geom, 0.9)),
    degrees(ST_Azimuth(ST_StartPoint(geom), ST_EndPoint(geom)))
FROM vialis.calles
WHERE costo BETWEEN 200 AND 500
  AND costo_inverso > 0
  AND ST_NPoints(geom) >= 2
ORDER BY id_calle
LIMIT 1
`).Scan(
		&streetID,
		&pathJSON,
		&searchArea,
		&startLongitude,
		&startLatitude,
		&endLongitude,
		&endLatitude,
		&directionDegrees,
	)
	if err != nil {
		t.Fatalf("prepare routing fixture: %v", err)
	}
	var path route.LineString
	if err := json.Unmarshal(pathJSON, &path); err != nil {
		t.Fatalf("decode routing path: %v", err)
	}
	trafficGeometry := make(orb.LineString, len(path.Positions))
	for index, position := range path.Positions {
		trafficGeometry[index] = orb.Point{position.Longitude, position.Latitude}
	}
	request := detour.RoutingRequest{
		Analysis: detour.Analysis{
			SearchArea:    searchArea,
			ForbiddenArea: distantForbiddenArea(),
		},
		Traffic: []traffic.Segment{{
			SourceID:     "integration-full",
			Geometry:     trafficGeometry,
			RoadCategory: "street",
			RoadCoverage: "full",
			SpeedKPH:     30,
			HasSpeed:     true,
		}},
		Points: []detour.RoutingPoint{
			{
				ID: 1,
				Position: route.Position{
					Longitude: startLongitude,
					Latitude:  startLatitude,
				},
				DirectionDegrees:      directionDegrees,
				MaximumDistanceMeters: 30,
				Role:                  detour.PointAnchor,
			},
			{
				ID: 2,
				Position: route.Position{
					Longitude: endLongitude,
					Latitude:  endLatitude,
				},
				DirectionDegrees:      directionDegrees,
				MaximumDistanceMeters: 30,
				Role:                  detour.PointAnchor,
			},
		},
		Pairs: []detour.PointPair{{From: 1, To: 2}, {From: 2, To: 1}},
	}

	result, err := NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if len(result.Paths) != 2 {
		t.Fatalf("paths = %d, want 2: %#v", len(result.Paths), result.Paths)
	}
	for _, got := range result.Paths {
		if got.TravelSeconds <= 0 || !slices.Contains(got.EdgeIDs, streetID) ||
			len(got.Geometry.Positions) < 2 {
			t.Fatalf("unexpected path: %#v", got)
		}
		first := got.Geometry.Positions[0]
		last := got.Geometry.Positions[len(got.Geometry.Positions)-1]
		if got.From == 1 {
			if route.DistanceMeters(first, request.Points[0].Position) > 1 ||
				route.DistanceMeters(last, request.Points[1].Position) > 1 {
				t.Fatalf("forward geometry is not oriented between virtual points: %#v", got)
			}
		} else if route.DistanceMeters(first, request.Points[1].Position) > 1 ||
			route.DistanceMeters(last, request.Points[0].Position) > 1 {
			t.Fatalf("reverse geometry is not oriented between virtual points: %#v", got)
		}
	}
	if result.Trace.ForwardDirectEdges == 0 || result.Trace.ReverseDirectEdges == 0 {
		t.Fatalf("direct traffic coverage missing: %#v", result.Trace)
	}
	if !slices.Contains(result.TopologyAvailablePairs, detour.PointPair{From: 1, To: 2}) ||
		!slices.Contains(result.TopologyAvailablePairs, detour.PointPair{From: 2, To: 1}) {
		t.Fatalf("topology pairs = %#v, want both directions", result.TopologyAvailablePairs)
	}

	request.Traffic[0].RoadCoverage = "one_side"
	oneSided, err := NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
	if err != nil {
		t.Fatalf("Route with one_side returned error: %v", err)
	}
	if len(oneSided.Paths) != 1 || oneSided.Paths[0].From != 1 || oneSided.Paths[0].To != 2 {
		t.Fatalf("one_side paths = %#v, want only the aligned direction", oneSided.Paths)
	}
	if !slices.Contains(oneSided.TopologyAvailablePairs, detour.PointPair{From: 2, To: 1}) {
		t.Fatalf("one_side topology pairs = %#v, want reverse diagnostic path", oneSided.TopologyAvailablePairs)
	}

	request.Analysis.BlockedStreetIDs = []int64{streetID}
	request.Points[0].MaximumDistanceMeters = 1
	request.Points[1].MaximumDistanceMeters = 1
	_, err = NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
	var detourError *detour.Error
	if !errors.As(err, &detourError) || detourError.Code != detour.ErrorAnchorNotMatched {
		t.Fatalf("Route with blocked edge error = %v, want anchor_not_matched", err)
	}

	request.Analysis.BlockedStreetIDs = nil
	request.Traffic[0].RoadCoverage = "full"
	request.Points[0].MaximumDistanceMeters = 30
	request.Points[1].MaximumDistanceMeters = 30
	request.Points = append(request.Points, detour.RoutingPoint{
		ID: 3,
		Position: route.Position{
			Longitude: startLongitude + 0.01,
			Latitude:  startLatitude + 0.01,
		},
		DirectionDegrees:      directionDegrees,
		MaximumDistanceMeters: 1,
		Role:                  detour.PointRequiredStop,
	})
	_, err = NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
	detourError = nil
	if !errors.As(err, &detourError) || detourError.Code != detour.ErrorRequiredStopNotMatched {
		t.Fatalf("Route with unmatched required stop error = %v", err)
	}
	request.Points[2].Role = detour.PointOptionalStop
	optional, err := NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
	if err != nil {
		t.Fatalf("Route with unmatched optional stop returned error: %v", err)
	}
	if !slices.Contains(optional.UnmatchedPointIDs, int64(3)) {
		t.Fatalf("unmatched optional points = %v, want 3", optional.UnmatchedPointIDs)
	}
}

func TestDetourRepositoryRouteRespectsOneWay(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	var (
		pathJSON, searchArea          []byte
		startLongitude, startLatitude float64
		endLongitude, endLatitude     float64
		directionDegrees              float64
	)
	err = pool.QueryRow(ctx, `
SELECT
    ST_AsGeoJSON(geom)::bytea,
    ST_AsGeoJSON(ST_Buffer(geom::geography, 5)::geometry)::bytea,
    ST_X(ST_LineInterpolatePoint(geom, 0.1)),
    ST_Y(ST_LineInterpolatePoint(geom, 0.1)),
    ST_X(ST_LineInterpolatePoint(geom, 0.9)),
    ST_Y(ST_LineInterpolatePoint(geom, 0.9)),
    degrees(ST_Azimuth(ST_StartPoint(geom), ST_EndPoint(geom)))
FROM vialis.calles
WHERE costo BETWEEN 100 AND 500
  AND costo_inverso = -1
ORDER BY id_calle
LIMIT 1
`).Scan(
		&pathJSON,
		&searchArea,
		&startLongitude,
		&startLatitude,
		&endLongitude,
		&endLatitude,
		&directionDegrees,
	)
	if err != nil {
		t.Fatalf("prepare one-way fixture: %v", err)
	}
	var line route.LineString
	if err := json.Unmarshal(pathJSON, &line); err != nil {
		t.Fatalf("decode one-way path: %v", err)
	}
	geometry := make(orb.LineString, len(line.Positions))
	for index, position := range line.Positions {
		geometry[index] = orb.Point{position.Longitude, position.Latitude}
	}
	request := detour.RoutingRequest{
		Analysis: detour.Analysis{
			SearchArea:    searchArea,
			ForbiddenArea: distantForbiddenArea(),
		},
		Traffic: []traffic.Segment{{
			SourceID: "one-way", Geometry: geometry, RoadCoverage: "full",
			SpeedKPH: 30, HasSpeed: true,
		}},
		Points: []detour.RoutingPoint{
			{
				ID:               1,
				Position:         route.Position{Longitude: startLongitude, Latitude: startLatitude},
				DirectionDegrees: directionDegrees, MaximumDistanceMeters: 1,
				Role: detour.PointAnchor,
			},
			{
				ID:               2,
				Position:         route.Position{Longitude: endLongitude, Latitude: endLatitude},
				DirectionDegrees: directionDegrees, MaximumDistanceMeters: 1,
				Role: detour.PointAnchor,
			},
		},
		Pairs: []detour.PointPair{{From: 1, To: 2}, {From: 2, To: 1}},
	}

	result, err := NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if len(result.Paths) != 1 || result.Paths[0].From != 1 || result.Paths[0].To != 2 {
		t.Fatalf("one-way paths = %#v, want only forward", result.Paths)
	}
}

func TestDetourRepositoryRouteUsesNearbyEstimate(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	var (
		pathJSON, searchArea          []byte
		offsetJSON                    [3][]byte
		startLongitude, startLatitude float64
		endLongitude, endLatitude     float64
		directionDegrees              float64
	)
	err = pool.QueryRow(ctx, `
SELECT
    ST_AsGeoJSON(geom)::bytea,
    ST_AsGeoJSON(ST_Buffer(geom::geography, 100)::geometry)::bytea,
    ST_AsGeoJSON(ST_Transform(ST_OffsetCurve(ST_Transform(geom, 3857), 30), 4326))::bytea,
    ST_AsGeoJSON(ST_Transform(ST_OffsetCurve(ST_Transform(geom, 3857), 45), 4326))::bytea,
    ST_AsGeoJSON(ST_Transform(ST_OffsetCurve(ST_Transform(geom, 3857), 60), 4326))::bytea,
    ST_X(ST_LineInterpolatePoint(geom, 0.1)),
    ST_Y(ST_LineInterpolatePoint(geom, 0.1)),
    ST_X(ST_LineInterpolatePoint(geom, 0.9)),
    ST_Y(ST_LineInterpolatePoint(geom, 0.9)),
    degrees(ST_Azimuth(ST_StartPoint(geom), ST_EndPoint(geom)))
FROM vialis.calles
WHERE costo BETWEEN 200 AND 500
  AND costo_inverso > 0
  AND tipo = 'residential'
  AND GeometryType(ST_OffsetCurve(ST_Transform(geom, 3857), 60)) = 'LINESTRING'
ORDER BY id_calle
LIMIT 1
`).Scan(
		&pathJSON,
		&searchArea,
		&offsetJSON[0],
		&offsetJSON[1],
		&offsetJSON[2],
		&startLongitude,
		&startLatitude,
		&endLongitude,
		&endLatitude,
		&directionDegrees,
	)
	if err != nil {
		t.Fatalf("prepare estimate fixture: %v", err)
	}
	trafficSegments := make([]traffic.Segment, len(offsetJSON))
	for index, encoded := range offsetJSON {
		var line route.LineString
		if err := json.Unmarshal(encoded, &line); err != nil {
			t.Fatalf("decode offset %d: %v", index, err)
		}
		geometry := make(orb.LineString, len(line.Positions))
		for pointIndex, position := range line.Positions {
			geometry[pointIndex] = orb.Point{position.Longitude, position.Latitude}
		}
		trafficSegments[index] = traffic.Segment{
			SourceID:     string(rune('A' + index)),
			Geometry:     geometry,
			RoadCategory: "street",
			RoadCoverage: "one_side",
			SpeedKPH:     float64(20 + index*10),
			HasSpeed:     true,
		}
	}
	request := detour.RoutingRequest{
		Analysis: detour.Analysis{
			SearchArea:    searchArea,
			ForbiddenArea: distantForbiddenArea(),
		},
		Traffic: trafficSegments,
		Points: []detour.RoutingPoint{
			{
				ID:               1,
				Position:         route.Position{Longitude: startLongitude, Latitude: startLatitude},
				DirectionDegrees: directionDegrees, MaximumDistanceMeters: 1,
				Role: detour.PointAnchor,
			},
			{
				ID:               2,
				Position:         route.Position{Longitude: endLongitude, Latitude: endLatitude},
				DirectionDegrees: directionDegrees, MaximumDistanceMeters: 1,
				Role: detour.PointAnchor,
			},
		},
		Pairs: []detour.PointPair{{From: 1, To: 2}},
	}

	result, err := NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if len(result.Paths) != 1 || result.Trace.ForwardEstimatedEdges == 0 {
		t.Fatalf("nearby estimate was not used: %#v", result)
	}
}

// consecutiveBlocks is two two-way blocks of the same street, joined end to
// start and a few degrees apart, with a traffic segment covering both.
type consecutiveBlocks struct {
	stopBlockID, nextBlockID int64
	searchArea               json.RawMessage
	traffic                  traffic.Segment
	// Points on the blocks at fixed fractions of their length, read in order
	// from a line through them.
	stopBlockAt        map[float64]route.Position
	nextBlockAt        map[float64]route.Position
	stopBlockDirection float64
	nextBlockDirection float64
}

func loadConsecutiveBlocks(ctx context.Context, t *testing.T, pool *pgxpool.Pool) consecutiveBlocks {
	t.Helper()
	var (
		fixture                      consecutiveBlocks
		pathJSON, searchArea         []byte
		stopBlockJSON, nextBlockJSON []byte
	)
	err := pool.QueryRow(ctx, `
SELECT
    stop_block.id_calle,
    next_block.id_calle,
    ST_AsGeoJSON(ST_LineMerge(ST_Collect(stop_block.geom, next_block.geom)))::bytea,
    ST_AsGeoJSON(ST_Buffer(ST_Collect(stop_block.geom, next_block.geom)::geography, 60)::geometry)::bytea,
    ST_AsGeoJSON(ST_MakeLine(ARRAY[
        ST_LineInterpolatePoint(stop_block.geom, 0.1),
        ST_LineInterpolatePoint(stop_block.geom, 0.6),
        ST_LineInterpolatePoint(stop_block.geom, 0.8)
    ]))::bytea,
    ST_AsGeoJSON(ST_MakeLine(
        ST_LineInterpolatePoint(next_block.geom, 0.5),
        ST_EndPoint(next_block.geom)
    ))::bytea,
    degrees(ST_Azimuth(ST_StartPoint(stop_block.geom), ST_EndPoint(stop_block.geom))),
    degrees(ST_Azimuth(ST_StartPoint(next_block.geom), ST_EndPoint(next_block.geom)))
FROM vialis.calles stop_block
JOIN vialis.calles next_block
  ON next_block.origen = stop_block.destino
 AND next_block.nombre = stop_block.nombre
 AND next_block.id_calle <> stop_block.id_calle
WHERE stop_block.costo_inverso > 0
  AND next_block.costo_inverso > 0
  AND ST_Length(stop_block.geom::geography) BETWEEN 80 AND 150
  AND ST_Length(next_block.geom::geography) BETWEEN 80 AND 150
  AND degrees(acos(LEAST(1.0, cos(
        ST_Azimuth(ST_StartPoint(stop_block.geom), ST_EndPoint(stop_block.geom))
        - ST_Azimuth(ST_StartPoint(next_block.geom), ST_EndPoint(next_block.geom))
      )))) BETWEEN 3 AND 20
ORDER BY stop_block.id_calle
LIMIT 1
`).Scan(
		&fixture.stopBlockID,
		&fixture.nextBlockID,
		&pathJSON,
		&searchArea,
		&stopBlockJSON,
		&nextBlockJSON,
		&fixture.stopBlockDirection,
		&fixture.nextBlockDirection,
	)
	if err != nil {
		t.Fatalf("prepare consecutive-block fixture: %v", err)
	}
	fixture.searchArea = searchArea
	decode := func(encoded []byte) route.LineString {
		var line route.LineString
		if err := json.Unmarshal(encoded, &line); err != nil {
			t.Fatalf("decode fixture line: %v", err)
		}
		return line
	}
	path := decode(pathJSON)
	geometry := make(orb.LineString, len(path.Positions))
	for index, position := range path.Positions {
		geometry[index] = orb.Point{position.Longitude, position.Latitude}
	}
	fixture.traffic = traffic.Segment{
		SourceID: "blocks", Geometry: geometry, RoadCoverage: "full",
		SpeedKPH: 30, HasSpeed: true,
	}
	fixture.stopBlockAt = pointsAt(decode(stopBlockJSON), 0.1, 0.6, 0.8)
	fixture.nextBlockAt = pointsAt(decode(nextBlockJSON), 0.5)
	return fixture
}

func pointsAt(points route.LineString, fractions ...float64) map[float64]route.Position {
	byFraction := make(map[float64]route.Position, len(fractions))
	for index, fraction := range fractions {
		byFraction[fraction] = points.Positions[index]
	}
	return byFraction
}

// A stop lying on one block must snap to that block even when the next block of
// the same street is a few degrees better aligned with the route: ranking by
// angle first dragged the stop to the next intersection, and the reconstructed
// path overshot the stop and drove back to it.
func TestDetourRepositoryRouteSnapsStopToNearestAlignedEdge(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	blocks := loadConsecutiveBlocks(ctx, t, pool)
	stop := blocks.stopBlockAt[0.6]
	request := detour.RoutingRequest{
		Analysis: detour.Analysis{
			SearchArea:    blocks.searchArea,
			ForbiddenArea: distantForbiddenArea(),
		},
		Traffic: []traffic.Segment{blocks.traffic},
		Points: []detour.RoutingPoint{
			{
				ID:               1,
				Position:         blocks.stopBlockAt[0.1],
				DirectionDegrees: blocks.stopBlockDirection, MaximumDistanceMeters: 1,
				Role: detour.PointAnchor,
			},
			{
				// Aligned with the next block, so only distance favours the
				// block the stop is actually on.
				ID:               2,
				Position:         stop,
				DirectionDegrees: blocks.nextBlockDirection, MaximumDistanceMeters: 150,
				Role: detour.PointRequiredStop,
			},
		},
		Pairs: []detour.PointPair{{From: 1, To: 2}},
	}

	result, err := NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if len(result.Paths) != 1 {
		t.Fatalf("paths = %#v, want one", result.Paths)
	}
	positions := result.Paths[0].Geometry.Positions
	snapped := positions[len(positions)-2]
	if distance := route.DistanceMeters(snapped, stop); distance > 1 {
		t.Fatalf("stop snapped %.1f m away from where it lies, want it on its own block", distance)
	}
}

// A stop whose own block is closed by the cut cannot be served from the next
// block: the bus would never pass it, and the connector drawn back to it ran
// along the closed street.
func TestDetourRepositoryRouteLeavesStopOnBlockedBlockUnmatched(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	blocks := loadConsecutiveBlocks(ctx, t, pool)
	request := detour.RoutingRequest{
		Analysis: detour.Analysis{
			SearchArea:       blocks.searchArea,
			ForbiddenArea:    distantForbiddenArea(),
			BlockedStreetIDs: []int64{blocks.stopBlockID},
		},
		Traffic: []traffic.Segment{blocks.traffic},
		Points: []detour.RoutingPoint{
			{
				ID:               1,
				Position:         blocks.nextBlockAt[0.5],
				DirectionDegrees: blocks.nextBlockDirection, MaximumDistanceMeters: 1,
				Role: detour.PointAnchor,
			},
			{
				ID:               2,
				Position:         blocks.stopBlockAt[0.8],
				DirectionDegrees: blocks.stopBlockDirection, MaximumDistanceMeters: 50,
				Role: detour.PointOptionalStop,
			},
		},
		Pairs: []detour.PointPair{{From: 1, To: 2}},
	}

	result, err := NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if !slices.Equal(result.UnmatchedPointIDs, []int64{2}) {
		t.Fatalf("unmatched = %v, want the stop on the blocked block; paths = %#v",
			result.UnmatchedPointIDs, result.Paths)
	}
}

func distantForbiddenArea() json.RawMessage {
	return json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0],[0,1],[1,1],[0,0]]]}`)
}

func TestDetourRepositoryAnalyzeRejectsCutOutsideGraph(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	path := route.LineString{Positions: []route.Position{
		{Latitude: 0, Longitude: 0},
		{Latitude: 0, Longitude: 0.001},
	}}
	input := route.Route{
		Jurisdiction: route.JurisdictionCABA,
		Stops: []route.Stop{
			{ID: "A", Position: path.Positions[0], PathToNext: &path},
			{ID: "B", Position: path.Positions[1]},
		},
	}
	cut := detour.Cut{LineString: route.LineString{Positions: []route.Position{
		{Latitude: 0, Longitude: 0},
		{Latitude: 0.001, Longitude: 0},
	}}}

	_, err = NewDetourRepository(pool).Analyze(ctx, input, cut, config.DetourPolicy())
	var detourError *detour.Error
	if !errors.As(err, &detourError) || detourError.Code != detour.ErrorCutOutsideGraph {
		t.Fatalf("Analyze error = %v, want cut_outside_graph", err)
	}
}

// turnRestriction is a no_left_turn of vialis.calles_restricciones between two
// short blocks, with every street around it priced by a uniform traffic
// snapshot so the only reason to avoid the turn is the restriction itself.
type turnRestriction struct {
	fromEdgeID, toEdgeID int64
	searchArea           json.RawMessage
	traffic              []traffic.Segment
	// Points on the from-block (driving towards the via vertex) and on the
	// to-block (driving away from it), at fixed fractions of their length.
	fromAt, toAt               map[float64]route.Position
	fromDirection, toDirection map[float64]float64
}

func loadTurnRestriction(ctx context.Context, t *testing.T, pool *pgxpool.Pool) turnRestriction {
	t.Helper()
	fixture := turnRestriction{
		fromAt:        map[float64]route.Position{},
		toAt:          map[float64]route.Position{},
		fromDirection: map[float64]float64{},
		toDirection:   map[float64]float64{},
	}
	var searchArea, trafficJSON []byte
	var fractions, fromLongitudes, fromLatitudes, fromDirections []float64
	var toLongitudes, toLatitudes, toDirections []float64
	err := pool.QueryRow(ctx, `
WITH restriction AS (
    SELECT
        r.id_calle_desde,
        r.id_calle_hacia,
        CASE WHEN desde.destino = r.id_vertice_via THEN desde.geom ELSE ST_Reverse(desde.geom) END AS from_line,
        CASE WHEN hacia.origen = r.id_vertice_via THEN hacia.geom ELSE ST_Reverse(hacia.geom) END AS to_line
    FROM vialis.calles_restricciones r
    JOIN vialis.calles desde ON desde.id_calle = r.id_calle_desde
    JOIN vialis.calles hacia ON hacia.id_calle = r.id_calle_hacia
    WHERE r.restriccion = 'no_left_turn'
      AND r.id_calle_desde <> r.id_calle_hacia
      AND ST_Length(desde.geom::geography) BETWEEN 60 AND 200
      AND ST_Length(hacia.geom::geography) BETWEEN 60 AND 200
    ORDER BY r.osm_relation_id
    LIMIT 1
), area AS (
    SELECT ST_Buffer(ST_Collect(from_line, to_line)::geography, 300)::geometry AS geom
    FROM restriction
), fractions AS (
    SELECT ARRAY[0.3, 0.8, 0.2, 0.7]::float8[] AS value
)
SELECT
    restriction.id_calle_desde,
    restriction.id_calle_hacia,
    ST_AsGeoJSON(area.geom)::bytea,
    (
        SELECT jsonb_agg(ST_AsGeoJSON(street.geom)::jsonb ORDER BY street.id_calle)
        FROM vialis.calles street
        WHERE street.geom && area.geom AND ST_Intersects(street.geom, area.geom)
    )::text::bytea,
    fractions.value,
    ARRAY(SELECT ST_X(ST_LineInterpolatePoint(restriction.from_line, f)) FROM unnest(fractions.value) f),
    ARRAY(SELECT ST_Y(ST_LineInterpolatePoint(restriction.from_line, f)) FROM unnest(fractions.value) f),
    ARRAY(SELECT degrees(ST_Azimuth(
        ST_LineInterpolatePoint(restriction.from_line, f - 0.05),
        ST_LineInterpolatePoint(restriction.from_line, f + 0.05)
    )) FROM unnest(fractions.value) f),
    ARRAY(SELECT ST_X(ST_LineInterpolatePoint(restriction.to_line, f)) FROM unnest(fractions.value) f),
    ARRAY(SELECT ST_Y(ST_LineInterpolatePoint(restriction.to_line, f)) FROM unnest(fractions.value) f),
    ARRAY(SELECT degrees(ST_Azimuth(
        ST_LineInterpolatePoint(restriction.to_line, f - 0.05),
        ST_LineInterpolatePoint(restriction.to_line, f + 0.05)
    )) FROM unnest(fractions.value) f)
FROM restriction, area, fractions
`).Scan(
		&fixture.fromEdgeID,
		&fixture.toEdgeID,
		&searchArea,
		&trafficJSON,
		&fractions,
		&fromLongitudes,
		&fromLatitudes,
		&fromDirections,
		&toLongitudes,
		&toLatitudes,
		&toDirections,
	)
	if err != nil {
		t.Fatalf("prepare turn-restriction fixture: %v", err)
	}
	fixture.searchArea = searchArea
	var lines []route.LineString
	if err := json.Unmarshal(trafficJSON, &lines); err != nil {
		t.Fatalf("decode turn-restriction streets: %v", err)
	}
	for index, line := range lines {
		geometry := make(orb.LineString, len(line.Positions))
		for pointIndex, position := range line.Positions {
			geometry[pointIndex] = orb.Point{position.Longitude, position.Latitude}
		}
		fixture.traffic = append(fixture.traffic, traffic.Segment{
			SourceID: fmt.Sprint(index), Geometry: geometry, RoadCoverage: "full",
			SpeedKPH: 30, HasSpeed: true,
		})
	}
	for index, fraction := range fractions {
		fixture.fromAt[fraction] = route.Position{Longitude: fromLongitudes[index], Latitude: fromLatitudes[index]}
		fixture.toAt[fraction] = route.Position{Longitude: toLongitudes[index], Latitude: toLatitudes[index]}
		fixture.fromDirection[fraction] = fromDirections[index]
		fixture.toDirection[fraction] = toDirections[index]
	}
	return fixture
}

// takesTurn reports whether a path leaves one edge straight into the other. An
// edge split by a routing point appears more than once in a row, so repeated
// ids are collapsed first.
func takesTurn(edgeIDs []int64, from, to int64) bool {
	compacted := slices.Compact(slices.Clone(edgeIDs))
	for index := 1; index < len(compacted); index++ {
		if compacted[index-1] == from && compacted[index] == to {
			return true
		}
	}
	return false
}

// The shortest way from one block to the other is the forbidden left turn; the
// path has to go around it. The second case adds stops on both blocks between
// the endpoints: pgRouting splits an edge at every point, and a restriction
// written on whole edges must still hold across those splits.
func TestDetourRepositoryRouteRespectsTurnRestrictions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	restriction := loadTurnRestriction(ctx, t, pool)
	anchor := func(id int64, position route.Position, direction float64, role detour.PointRole) detour.RoutingPoint {
		return detour.RoutingPoint{
			ID: id, Position: position, DirectionDegrees: direction,
			MaximumDistanceMeters: 1, Role: role,
		}
	}
	endpoints := []detour.RoutingPoint{
		anchor(1, restriction.fromAt[0.3], restriction.fromDirection[0.3], detour.PointAnchor),
		anchor(4, restriction.toAt[0.7], restriction.toDirection[0.7], detour.PointAnchor),
	}
	intermediate := []detour.RoutingPoint{
		anchor(2, restriction.fromAt[0.8], restriction.fromDirection[0.8], detour.PointOptionalStop),
		anchor(3, restriction.toAt[0.2], restriction.toDirection[0.2], detour.PointOptionalStop),
	}
	cases := map[string][]detour.RoutingPoint{
		"endpoints only":          endpoints,
		"stops split both blocks": append(slices.Clone(endpoints), intermediate...),
	}
	for name, points := range cases {
		t.Run(name, func(t *testing.T) {
			request := detour.RoutingRequest{
				Analysis: detour.Analysis{
					SearchArea:    restriction.searchArea,
					ForbiddenArea: distantForbiddenArea(),
				},
				Traffic: restriction.traffic,
				Points:  points,
				Pairs:   []detour.PointPair{{From: 1, To: 4}},
			}
			result, err := NewDetourRepository(pool).Route(ctx, request, config.DetourPolicy())
			if err != nil {
				t.Fatalf("Route returned error: %v", err)
			}
			if len(result.UnmatchedPointIDs) != 0 {
				t.Fatalf("unmatched = %v, want every point on its block", result.UnmatchedPointIDs)
			}
			if len(result.Paths) != 1 {
				t.Fatalf("paths = %#v, want one path around the restriction", result.Paths)
			}
			if edges := result.Paths[0].EdgeIDs; takesTurn(edges, restriction.fromEdgeID, restriction.toEdgeID) {
				t.Fatalf("path %v turns from %d into %d, which OSM forbids",
					edges, restriction.fromEdgeID, restriction.toEdgeID)
			}
			if !slices.Contains(result.TopologyAvailablePairs, detour.PointPair{From: 1, To: 4}) {
				t.Fatalf("topology pairs = %#v, want 1 -> 4", result.TopologyAvailablePairs)
			}
		})
	}
}
