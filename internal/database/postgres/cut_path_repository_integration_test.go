package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A cut drawn along three blocks of a one-way street, against its direction,
// follows those blocks instead of driving around them, and goes through the
// middle waypoint.
func TestCutPathServiceFollowsOneWayStreetsAgainstTheirDirectionIntegration(t *testing.T) {
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

	var firstJSON, middleJSON, lastJSON []byte
	var expectedLength float64
	err = pool.QueryRow(ctx, `
		SELECT
		    ST_AsGeoJSON(ST_LineInterpolatePoint(following.geom, 0.5))::bytea,
		    ST_AsGeoJSON(ST_LineInterpolatePoint(middle.geom, 0.5))::bytea,
		    ST_AsGeoJSON(ST_LineInterpolatePoint(previous.geom, 0.5))::bytea,
		    ST_Length(previous.geom::geography) / 2
		        + ST_Length(middle.geom::geography)
		        + ST_Length(following.geom::geography) / 2
		FROM vialis.calles middle
		JOIN vialis.calles previous
		  ON previous.destino = middle.origen AND previous.nombre = middle.nombre
		 AND previous.id_calle <> middle.id_calle AND previous.costo_inverso = -1
		JOIN vialis.calles following
		  ON following.origen = middle.destino AND following.nombre = middle.nombre
		 AND following.id_calle NOT IN (middle.id_calle, previous.id_calle) AND following.costo_inverso = -1
		WHERE middle.costo_inverso = -1
		  AND middle.tipo IN ('residential', 'tertiary')
		  AND ST_Length(middle.geom::geography) BETWEEN 80 AND 130
		  AND ST_Length(previous.geom::geography) BETWEEN 80 AND 130
		  AND ST_Length(following.geom::geography) BETWEEN 80 AND 130
		  AND ST_Y(ST_StartPoint(middle.geom)) BETWEEN -34.62 AND -34.58
		  AND ST_X(ST_StartPoint(middle.geom)) BETWEEN -58.46 AND -58.40
		ORDER BY middle.id_calle
		LIMIT 1
	`).Scan(&firstJSON, &middleJSON, &lastJSON, &expectedLength)
	if err != nil {
		t.Fatalf("prepare cut path fixture: %v", err)
	}
	first := decodePoint(t, firstJSON)
	middle := decodePoint(t, middleJSON)
	last := decodePoint(t, lastJSON)

	service, err := detour.NewCutPathService(NewDetourRepository(pool), config.CutPathPolicy())
	if err != nil {
		t.Fatalf("NewCutPathService() error = %v", err)
	}

	path, err := service.Route(ctx, detour.CutPathInput{Waypoints: []route.Position{first, middle, last}})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if path.DistanceMeters > expectedLength+5 || path.DistanceMeters < expectedLength-5 {
		t.Fatalf("distance = %.1f m, want the %.1f m along the street", path.DistanceMeters, expectedLength)
	}
	assertEndpoints(t, path.Geometry, first, last)
	var passesMiddle bool
	for _, position := range path.Geometry.Positions {
		if route.DistanceMeters(position, middle) < 1 {
			passesMiddle = true
			break
		}
	}
	if !passesMiddle {
		t.Fatal("path does not go through the middle waypoint")
	}
	for index := 1; index < len(path.Geometry.Positions); index++ {
		if path.Geometry.Positions[index] == path.Geometry.Positions[index-1] {
			t.Fatalf("position %d repeats the previous one", index)
		}
	}
}

func TestCutPathServiceNamesTheWaypointFarFromStreetsIntegration(t *testing.T) {
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

	service, err := detour.NewCutPathService(NewDetourRepository(pool), config.CutPathPolicy())
	if err != nil {
		t.Fatalf("NewCutPathService() error = %v", err)
	}

	// The second waypoint is in the Río de la Plata, kilometres off the coast.
	_, err = service.Route(ctx, detour.CutPathInput{Waypoints: []route.Position{
		{Latitude: -34.5616, Longitude: -58.4431},
		{Latitude: -34.5300, Longitude: -58.3900},
	}})

	var domainError *detour.Error
	if !errors.As(err, &domainError) || domainError.Code != detour.ErrorWaypointNotMatched {
		t.Fatalf("error = %v, want waypoint_not_matched", err)
	}
	if domainError.Field != "waypoints[1]" {
		t.Fatalf("field = %q, want waypoints[1]", domainError.Field)
	}
}
