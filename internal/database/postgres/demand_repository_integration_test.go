package postgres

import (
	"context"
	"math"
	"os"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDemandRepositoryIntegration(t *testing.T) {
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

	repository := newDemandRepository(func(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (rowIterator, error) {
		return transaction.Query(ctx, sql, arguments...)
	})

	stops := []route.Stop{
		{ID: "A", Position: route.Position{Latitude: -40, Longitude: -50}},
		{ID: "B", Position: route.Position{Latitude: -40, Longitude: -40}},
		{ID: "C", Position: route.Position{Latitude: -40, Longitude: -30}},
	}
	cellIDs := make([]string, len(stops))
	for index, stop := range stops {
		if err := transaction.QueryRow(ctx, `
            SELECT h3_lat_lng_to_cell(
                ST_SetSRID(ST_MakePoint($1, $2), 4326),
                8
            )::TEXT
        `, stop.Position.Longitude, stop.Position.Latitude).Scan(&cellIDs[index]); err != nil {
			t.Fatalf("calculate fixture cell %s: %v", stop.ID, err)
		}
	}

	insertHexagon := func(index int, hotspotDistance float64) {
		t.Helper()
		stop := stops[index]
		_, err := transaction.Exec(ctx, `
            INSERT INTO vialis.hexagonos_viajes (
                indice_h3,
                punto_maxima_concurrencia,
                concurrencia
            )
            VALUES (
                $1::h3index,
                ST_Project(
                    ST_SetSRID(ST_MakePoint($2, $3), 4326)::geography,
                    $4,
                    0
                )::geometry,
                1
            )
            ON CONFLICT (indice_h3) DO UPDATE
            SET punto_maxima_concurrencia = EXCLUDED.punto_maxima_concurrencia,
                concurrencia = EXCLUDED.concurrencia
        `, cellIDs[index], stop.Position.Longitude, stop.Position.Latitude, hotspotDistance)
		if err != nil {
			t.Fatalf("insert fixture hexagon %s: %v", stop.ID, err)
		}
	}
	insertHexagon(0, 0)
	insertHexagon(1, 400)
	insertHexagon(2, config.SimulationAccessRadiusMeters+1)

	candidates, err := repository.FindCellCandidates(
		ctx,
		route.Route{Stops: stops},
		config.SimulationAccessRadiusMeters,
	)
	if err != nil {
		t.Fatalf("FindCellCandidates() error = %v", err)
	}
	assertCandidate(t, candidates, "A", cellIDs[0], 0)
	assertCandidate(t, candidates, "B", cellIDs[1], 400)
	for _, candidate := range candidates {
		if candidate.StopID == "C" && string(candidate.CellID) == cellIDs[2] {
			t.Fatal("cell at or beyond the access radius must be excluded")
		}
	}

	insertMatrixDemand := func(origin, destination int, trips float64) {
		t.Helper()
		_, err := transaction.Exec(ctx, `
            INSERT INTO vialis.matriz_origen_destino (
                h3_origen,
                h3_destino,
                cantidad_viajes
            )
            VALUES ($1::h3index, $2::h3index, $3)
            ON CONFLICT (h3_origen, h3_destino) DO UPDATE
            SET cantidad_viajes = EXCLUDED.cantidad_viajes
        `, cellIDs[origin], cellIDs[destination], trips)
		if err != nil {
			t.Fatalf("insert fixture matrix demand: %v", err)
		}
	}
	insertMatrixDemand(0, 1, 100)
	insertMatrixDemand(0, 2, 50)
	insertMatrixDemand(1, 2, 25)
	insertMatrixDemand(2, 0, 999)

	pairDemand, err := repository.FindDemandByStopPair(ctx, []demand.AssignedCell{
		{StopOrder: 0, StopID: "A", CellID: demand.CellID(cellIDs[0]), Accessibility: 1},
		{StopOrder: 1, StopID: "B", CellID: demand.CellID(cellIDs[1]), Accessibility: 0.5},
		{StopOrder: 2, StopID: "C", CellID: demand.CellID(cellIDs[2]), Accessibility: 0.25},
	})
	if err != nil {
		t.Fatalf("FindDemandByStopPair() error = %v", err)
	}
	if len(pairDemand) != 3 {
		t.Fatalf("pair demand count = %d, want 3", len(pairDemand))
	}
	assertPairDemand(t, pairDemand, "A", "B", 100, 50)
	assertPairDemand(t, pairDemand, "A", "C", 50, 12.5)
	assertPairDemand(t, pairDemand, "B", "C", 25, 3.125)
}

func assertCandidate(
	t *testing.T,
	candidates []demand.CellCandidate,
	stopID, cellID string,
	distance float64,
) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.StopID == stopID && string(candidate.CellID) == cellID {
			if math.Abs(candidate.DistanceMeters-distance) > 0.01 {
				t.Fatalf("candidate distance = %v, want %v", candidate.DistanceMeters, distance)
			}
			return
		}
	}
	t.Fatalf("candidate %s/%s not found", stopID, cellID)
}

func assertPairDemand(
	t *testing.T,
	pairs []demand.StopPairDemand,
	origin, destination string,
	gross, potential float64,
) {
	t.Helper()
	for _, pair := range pairs {
		if pair.OriginStopID == origin && pair.DestinationStopID == destination {
			if math.Abs(pair.GrossDemand-gross) > 1e-9 {
				t.Fatalf("%s->%s gross demand = %v, want %v", origin, destination, pair.GrossDemand, gross)
			}
			if math.Abs(pair.PotentialDemand-potential) > 1e-9 {
				t.Fatalf("%s->%s potential demand = %v, want %v", origin, destination, pair.PotentialDemand, potential)
			}
			return
		}
	}
	t.Fatalf("pair demand %s->%s not found", origin, destination)
}
