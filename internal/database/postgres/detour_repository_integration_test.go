package postgres

import (
	"context"
	"encoding/json"
	"errors"
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
