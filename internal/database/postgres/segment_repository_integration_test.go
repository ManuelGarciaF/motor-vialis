package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A segment along one block follows that block, and the same segment with the
// block cut in the middle goes around it without touching the cut.
func TestSegmentServiceRoutesAroundTheCutIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	var originJSON, destinationJSON, cutJSON []byte
	var blockLength float64
	// Three consecutive blocks of one street: the segment runs from the first
	// to the last, and the cut closes the middle one.
	err = pool.QueryRow(ctx, `
		SELECT
		    ST_AsGeoJSON(ST_LineInterpolatePoint(previous.geom, 0.5))::bytea,
		    ST_AsGeoJSON(ST_LineInterpolatePoint(following.geom, 0.5))::bytea,
		    ST_AsGeoJSON(ST_MakeLine(
		        ST_Project(ST_LineInterpolatePoint(middle.geom, 0.5)::geography, 15,
		            ST_Azimuth(ST_StartPoint(middle.geom), ST_EndPoint(middle.geom)) + pi() / 2)::geometry,
		        ST_Project(ST_LineInterpolatePoint(middle.geom, 0.5)::geography, 15,
		            ST_Azimuth(ST_StartPoint(middle.geom), ST_EndPoint(middle.geom)) - pi() / 2)::geometry
		    ))::bytea,
		    ST_Length(previous.geom::geography) / 2
		        + ST_Length(middle.geom::geography)
		        + ST_Length(following.geom::geography) / 2
		FROM vialis.calles middle
		JOIN vialis.calles previous
		  ON previous.destino = middle.origen AND previous.nombre = middle.nombre
		 AND previous.id_calle <> middle.id_calle AND previous.costo > 0
		JOIN vialis.calles following
		  ON following.origen = middle.destino AND following.nombre = middle.nombre
		 AND following.id_calle NOT IN (middle.id_calle, previous.id_calle) AND following.costo > 0
		WHERE middle.costo > 0
		  AND middle.tipo IN ('residential', 'tertiary')
		  AND ST_Length(middle.geom::geography) BETWEEN 80 AND 130
		  AND ST_Length(previous.geom::geography) BETWEEN 80 AND 130
		  AND ST_Length(following.geom::geography) BETWEEN 80 AND 130
		  AND ST_Y(ST_StartPoint(middle.geom)) BETWEEN -34.62 AND -34.58
		  AND ST_X(ST_StartPoint(middle.geom)) BETWEEN -58.46 AND -58.40
		ORDER BY middle.id_calle
		LIMIT 1
	`).Scan(&originJSON, &destinationJSON, &cutJSON, &blockLength)
	if err != nil {
		t.Fatalf("prepare segment fixture: %v", err)
	}
	origin := decodePoint(t, originJSON)
	destination := decodePoint(t, destinationJSON)
	var cut route.LineString
	if err := json.Unmarshal(cutJSON, &cut); err != nil {
		t.Fatalf("decode cut: %v", err)
	}

	service, err := detour.NewSegmentService(NewDetourRepository(pool), config.DetourPolicy(), config.SegmentPolicy())
	if err != nil {
		t.Fatalf("NewSegmentService() error = %v", err)
	}
	input := detour.SegmentInput{Waypoints: []route.Position{origin, destination}}

	direct, err := service.Route(ctx, input)
	if err != nil {
		t.Fatalf("Route() without cut error = %v", err)
	}
	// Snapping may add a few metres at each end.
	if direct.DistanceMeters > blockLength+20 {
		t.Fatalf("direct distance = %.0f m, want it along the %.0f m street", direct.DistanceMeters, blockLength)
	}
	assertEndpoints(t, direct.Geometry, origin, destination)

	input.Avoid = &detour.Cut{Lines: []route.LineString{cut}}
	around, err := service.Route(ctx, input)
	var domainError *detour.Error
	if errors.As(err, &domainError) && domainError.Code == detour.ErrorNoSegmentRoute {
		t.Skipf("fixture block has no way around it: %v", err)
	}
	if err != nil {
		t.Fatalf("Route() with cut error = %v", err)
	}
	assertEndpoints(t, around.Geometry, origin, destination)
	if around.DistanceMeters <= direct.DistanceMeters {
		t.Fatalf("detoured distance = %.0f m, want more than the direct %.0f m", around.DistanceMeters, direct.DistanceMeters)
	}

	var crosses bool
	encoded, _ := json.Marshal(around.Geometry)
	if err := pool.QueryRow(ctx, `
		SELECT ST_DWithin(
		    ST_SetSRID(ST_GeomFromGeoJSON($1::jsonb), 4326)::geography,
		    ST_SetSRID(ST_GeomFromGeoJSON($2::jsonb), 4326)::geography,
		    $3::double precision
		)
	`, string(encoded), string(cutJSON), config.DetourForbiddenCorridorMeters).Scan(&crosses); err != nil {
		t.Fatalf("check cut crossing: %v", err)
	}
	if crosses {
		t.Fatal("detoured segment crosses the cut corridor")
	}
}

func decodePoint(t *testing.T, encoded []byte) route.Position {
	t.Helper()
	var point struct {
		Coordinates [2]float64 `json:"coordinates"`
	}
	if err := json.Unmarshal(encoded, &point); err != nil {
		t.Fatalf("decode point: %v", err)
	}
	return route.Position{Longitude: point.Coordinates[0], Latitude: point.Coordinates[1]}
}

func assertEndpoints(t *testing.T, geometry route.LineString, origin, destination route.Position) {
	t.Helper()
	positions := geometry.Positions
	if len(positions) < 2 {
		t.Fatalf("geometry has %d positions", len(positions))
	}
	if distance := route.DistanceMeters(positions[0], origin); distance > 20 {
		t.Fatalf("geometry starts %.1f m from the origin", distance)
	}
	if distance := route.DistanceMeters(positions[len(positions)-1], destination); distance > 20 {
		t.Fatalf("geometry ends %.1f m from the destination", distance)
	}
}
