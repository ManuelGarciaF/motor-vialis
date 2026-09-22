package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/paulmach/orb"
)

func TestDetourServiceIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, `
SELECT
    middle.id_calle,
    ST_AsGeoJSON(ST_MakeLine(ARRAY[incoming.geom, middle.geom, outgoing.geom]))::bytea,
    ST_AsGeoJSON(ST_MakeLine(
        (ST_Project(
            ST_LineInterpolatePoint(middle.geom, 0.5)::geography,
            20,
            ST_Azimuth(ST_StartPoint(middle.geom), ST_EndPoint(middle.geom)) + pi() / 2
        ))::geometry,
        (ST_Project(
            ST_LineInterpolatePoint(middle.geom, 0.5)::geography,
            20,
            ST_Azimuth(ST_StartPoint(middle.geom), ST_EndPoint(middle.geom)) - pi() / 2
        ))::geometry
    ))::bytea
FROM vialis.calles middle
JOIN LATERAL (
    SELECT candidate.geom
    FROM vialis.calles candidate
    WHERE candidate.destino = middle.origen
      AND candidate.costo > 0
      AND candidate.id_calle <> middle.id_calle
    ORDER BY candidate.id_calle
    LIMIT 1
) incoming ON true
JOIN LATERAL (
    SELECT candidate.geom
    FROM vialis.calles candidate
    WHERE candidate.origen = middle.destino
      AND candidate.costo > 0
      AND candidate.id_calle <> middle.id_calle
    ORDER BY candidate.id_calle
    LIMIT 1
) outgoing ON true
WHERE middle.costo_inverso > 0
  AND ST_Length(middle.geom::geography) BETWEEN 50 AND 200
  AND middle.geom && ST_MakeEnvelope(-58.45, -34.63, -58.37, -34.57, 4326)
ORDER BY middle.id_calle
LIMIT 8
`)
	if err != nil {
		t.Fatalf("query route candidates: %v", err)
	}
	type candidate struct {
		blockedID int64
		path      route.LineString
		cut       route.LineString
	}
	var candidates []candidate
	for rows.Next() {
		var blockedID int64
		var pathJSON, cutJSON []byte
		if err := rows.Scan(&blockedID, &pathJSON, &cutJSON); err != nil {
			rows.Close()
			t.Fatalf("scan route candidate: %v", err)
		}
		var path, cut route.LineString
		if err := json.Unmarshal(pathJSON, &path); err != nil {
			t.Fatalf("decode candidate path: %v", err)
		}
		if err := json.Unmarshal(cutJSON, &cut); err != nil {
			t.Fatalf("decode candidate cut: %v", err)
		}
		candidates = append(candidates, candidate{blockedID: blockedID, path: path, cut: cut})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate route candidates: %v", err)
	}
	rows.Close()

	var attemptErrors []error
	for _, candidate := range candidates {
		segments, err := trafficAroundCut(ctx, pool, candidate.cut)
		if err != nil {
			t.Fatalf("load traffic fixture: %v", err)
		}
		inputRoute := route.Route{
			Jurisdiction: route.JurisdictionCABA,
			Stops: []route.Stop{
				{ID: "origin", Position: candidate.path.Positions[0], PathToNext: &candidate.path},
				{ID: "destination", Position: candidate.path.Positions[len(candidate.path.Positions)-1]},
			},
		}
		provider := &integrationTrafficProvider{segments: segments}
		service, err := detour.NewService(
			NewDetourRepository(pool),
			provider,
			integrationComparator{},
			config.DetourPolicy(),
		)
		if err != nil {
			t.Fatalf("create detour service: %v", err)
		}
		result, err := service.Plan(ctx, detour.Input{
			Route:     inputRoute,
			Cut:       detour.Cut{LineString: candidate.cut},
			Criterion: detour.CriterionShortestTime,
		})
		if err != nil {
			attemptErrors = append(attemptErrors, fmt.Errorf("edge %d: %w", candidate.blockedID, err))
			continue
		}
		if err := route.Validate(result.Variant); err != nil {
			t.Fatalf("generated variant is invalid: %v", err)
		}
		if len(result.Variant.Stops) != 2 || result.Trace.DecisionTravelSeconds <= 0 ||
			len(result.Trace.BlockedStreetIDs) == 0 || provider.tileCount == 0 {
			t.Fatalf("incomplete service result: %#v", result.Trace)
		}
		return
	}
	t.Fatalf("no candidate produced a complete detour: %v", attemptErrors)
}

func trafficAroundCut(
	ctx context.Context,
	pool *pgxpool.Pool,
	cut route.LineString,
) ([]traffic.Segment, error) {
	encodedCut, err := json.Marshal(cut)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
WITH area AS (
    SELECT ST_Buffer(
        ST_SetSRID(ST_GeomFromGeoJSON($1::jsonb), 4326)::geography,
        1000
    )::geometry AS geom
)
SELECT street.id_calle, ST_AsGeoJSON(street.geom)::bytea
FROM vialis.calles street
CROSS JOIN area
WHERE street.geom && ST_Envelope(area.geom)
  AND ST_Intersects(street.geom, area.geom)
ORDER BY street.id_calle
`, encodedCut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []traffic.Segment
	for rows.Next() {
		var id int64
		var geometryJSON []byte
		if err := rows.Scan(&id, &geometryJSON); err != nil {
			return nil, err
		}
		var line route.LineString
		if err := json.Unmarshal(geometryJSON, &line); err != nil {
			return nil, err
		}
		geometry := make(orb.LineString, len(line.Positions))
		for index, position := range line.Positions {
			geometry[index] = orb.Point{position.Longitude, position.Latitude}
		}
		result = append(result, traffic.Segment{
			SourceID:     strconv.FormatInt(id, 10),
			Geometry:     geometry,
			RoadCoverage: "full",
			SpeedKPH:     30,
			HasSpeed:     true,
		})
	}
	return result, rows.Err()
}

type integrationTrafficProvider struct {
	segments  []traffic.Segment
	tileCount int
}

func (provider *integrationTrafficProvider) Snapshot(
	_ context.Context,
	tiles []traffic.Tile,
) (traffic.Snapshot, error) {
	provider.tileCount = len(tiles)
	return traffic.Snapshot{
		Segments:  provider.segments,
		Zoom:      14,
		TileCount: len(tiles),
	}, nil
}

type integrationComparator struct{}

func (integrationComparator) CompareDetour(
	_ context.Context,
	input simulation.ComparisonInput,
) (simulation.Comparison, error) {
	baseline := make([]simulation.StopResult, len(input.Baseline.Stops))
	for index, stop := range input.Baseline.Stops {
		baseline[index] = simulation.StopResult{StopOrder: index, StopID: stop.ID}
	}
	return simulation.Comparison{Baseline: simulation.Result{ByStop: baseline}}, nil
}
