package postgres

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTravelTimeRepositoryIntegration(t *testing.T) {
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

	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()

	repository := newTravelTimeRepository(func(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (rowIterator, error) {
		return transaction.Query(ctx, sql, arguments...)
	})

	fixturePrefix := fmt.Sprintf("traveltime-integration-%d", time.Now().UnixNano())
	forwardWKT := "LINESTRING(-50 -40, -49.99 -40)"
	reverseWKT := "LINESTRING(-49.99 -40, -50 -40)"
	forwardRouteID := insertTravelTimeReference(
		t,
		ctx,
		transaction,
		fixturePrefix+"-forward",
		forwardWKT,
		80,
		100,
		130,
	)
	reverseRouteID := insertTravelTimeReference(
		t,
		ctx,
		transaction,
		fixturePrefix+"-reverse",
		reverseWKT,
		90,
		110,
		140,
	)

	inputPath := route.LineString{Positions: []route.Position{
		{Latitude: -40, Longitude: -50},
		{Latitude: -40, Longitude: -49.99},
	}}
	measured, err := repository.FindSegmentReferences(
		ctx,
		[]traveltime.Segment{{
			Order:             0,
			OriginStopID:      "A",
			DestinationStopID: "B",
			Path:              inputPath,
		}},
		config.DefaultTravelTimePolicy(),
	)
	if err != nil {
		t.Fatalf("FindSegmentReferences() error = %v", err)
	}
	if len(measured) != 1 {
		t.Fatalf("segments = %d, want 1", len(measured))
	}
	if math.Abs(measured[0].LengthMeters-853.94) > 2 {
		t.Fatalf("length = %v, want approximately 853.94", measured[0].LengthMeters)
	}
	if len(measured[0].References) != 3 {
		t.Fatalf("references = %#v, want forward reference at three radii", measured[0].References)
	}
	for _, reference := range measured[0].References {
		if reference.RouteID != forwardRouteID {
			t.Fatalf(
				"reference route = %d, want %d; reverse route %d must be excluded",
				reference.RouteID,
				forwardRouteID,
				reverseRouteID,
			)
		}
		if reference.Paces.OffPeak <= 0 ||
			reference.Paces.OffPeak > reference.Paces.Typical ||
			reference.Paces.Typical > reference.Paces.Peak {
			t.Fatalf("reference paces = %#v", reference.Paces)
		}
	}

	global, err := repository.FindGlobalPaces(ctx, config.DefaultTravelTimePolicy())
	if err != nil {
		t.Fatalf("FindGlobalPaces() error = %v", err)
	}
	if global.RouteCount < 2 ||
		global.Paces.OffPeak <= 0 ||
		global.Paces.OffPeak > global.Paces.Typical ||
		global.Paces.Typical > global.Paces.Peak {
		t.Fatalf("global paces = %#v", global)
	}
}

func insertTravelTimeReference(
	t *testing.T,
	ctx context.Context,
	transaction pgx.Tx,
	id string,
	pathWKT string,
	offPeakSeconds, typicalSeconds, peakSeconds int,
) int64 {
	t.Helper()

	var routeID int64
	if err := transaction.QueryRow(ctx, `
        WITH path AS (
            SELECT ST_GeomFromText($4, 4326)
                ::GEOMETRY(LineString, 4326) AS geom
        )
        INSERT INTO vialis.recorridos (
            gtfs_route_id,
            gtfs_shape_id,
            nombre_publico,
            linea,
            ramal,
            direction_id,
            geom,
            distancia_metros,
            tiempo_total_minutos
        )
        SELECT
            $1,
            $2,
            $3,
            $3,
            'TRONCAL',
            0,
            path.geom,
            ROUND(ST_Length(path.geom::geography))::INTEGER,
            10
        FROM path
        RETURNING id_recorrido
    `, id, id+"-shape", id, pathWKT).Scan(&routeID); err != nil {
		t.Fatalf("insert fixture route: %v", err)
	}

	var stopID int64
	if err := transaction.QueryRow(ctx, `
        INSERT INTO vialis.paradas (
            gtfs_stop_id,
            nombre,
            posicion
        )
        VALUES (
            $1,
            $1,
            ST_StartPoint(
                ST_GeomFromText($2, 4326)
                    ::GEOMETRY(LineString, 4326)
            )
        )
        RETURNING id_parada
    `, id+"-stop", pathWKT).Scan(&stopID); err != nil {
		t.Fatalf("insert fixture stop: %v", err)
	}

	if _, err := transaction.Exec(ctx, `
        WITH path AS (
            SELECT ST_GeomFromText($3, 4326)
                ::GEOMETRY(LineString, 4326) AS geom
        )
        INSERT INTO vialis.recorridos_paradas (
            id_recorrido,
            id_parada,
            nro_parada,
            tramo_hasta_siguiente,
            distancia_hasta_siguiente_metros,
            tiempo_valle_hasta_siguiente_segundos,
            tiempo_tipico_hasta_siguiente_segundos,
            tiempo_pico_hasta_siguiente_segundos,
            cantidad_muestras_tiempo
        )
        SELECT
            $1,
            $2,
            0,
            path.geom,
            ROUND(ST_Length(path.geom::geography))::INTEGER,
            $4,
            $5,
            $6,
            20
        FROM path
    `, routeID, stopID, pathWKT, offPeakSeconds, typicalSeconds, peakSeconds); err != nil {
		t.Fatalf("insert fixture route stop: %v", err)
	}
	return routeID
}
