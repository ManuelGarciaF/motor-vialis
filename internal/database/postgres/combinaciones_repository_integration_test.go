package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The fixtures sit on the equator at longitude 10, far from the AMBA feed, for
// the same reason the similarity fixtures do: nothing real can reach them.
// Their estimate is absurd so the pair ranks first whatever the database
// already holds, and every id is far above what the identity sequences have
// issued.
const (
	rankingLatitude     = 0.0
	rankingLongitude    = 10.0
	rankingVolume       = 1_000_000_000.0
	rankingHour         = 9
	rankingFirstLineID  = 9_100_001
	rankingSecondLineID = 9_100_002
	rankingBoardStopID  = 9_100_001
	rankingAlightStopID = 9_100_002
)

// TestCombinacionesRankingIntegration checks the read query against a real
// PostGIS and H3: the two banda-horaria semantics (a concrete hour and the
// whole-day row, which is a NULL that means something), the window functions
// that carry the total and the maximum, and the JSON aggregate of top flows.
//
// It does not exercise sql/viajes/combinaciones_lineas.sql. That script
// truncates and recomputes the whole aggregate over every flow in the city, so
// running it here would either take minutes or destroy a developer's database.
// Its semantics are verified by a full-dataset run instead.
//
// Everything runs inside a transaction that is rolled back.
func TestCombinacionesRankingIntegration(t *testing.T) {
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

	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()

	repository := newCombinacionesRepository(func(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (rowIterator, error) {
		return transaction.Query(ctx, sql, arguments...)
	})

	insertRankingFixture(ctx, t, transaction)

	t.Run("the whole day is a row and not a missing filter", func(t *testing.T) {
		found, total, maximum, err := repository.FindRanking(
			ctx,
			combinaciones.Query{Hour: nil, Limit: 5},
		)
		if err != nil {
			t.Fatalf("FindRanking() error = %v", err)
		}
		if len(found) == 0 {
			t.Fatal("no combination reported, want the fixture first")
		}
		if found[0].EstimatedTrips != rankingVolume {
			t.Fatalf(
				"first estimate = %v, want the fixture's %v",
				found[0].EstimatedTrips, rankingVolume,
			)
		}
		if found[0].First.LineID != rankingFirstLineID ||
			found[0].Second.LineID != rankingSecondLineID {
			t.Errorf("first pair = %d -> %d", found[0].First.LineID, found[0].Second.LineID)
		}
		if found[0].PeakHour != rankingHour {
			t.Errorf("peak hour = %d, want %d", found[0].PeakHour, rankingHour)
		}
		if total < 1 {
			t.Errorf("total = %d, want at least the fixture", total)
		}
		if maximum != rankingVolume {
			t.Errorf("maximum = %v, want %v", maximum, rankingVolume)
		}
		if len(found[0].TopFlows) != 1 {
			t.Fatalf("top flows = %#v, want 1", found[0].TopFlows)
		}
		if found[0].TopFlows[0].Alternatives != 2 {
			t.Errorf("flow alternatives = %d, want 2", found[0].TopFlows[0].Alternatives)
		}
		if found[0].Transfer.WalkMeters < 0 {
			t.Errorf("transfer = %#v", found[0].Transfer)
		}
	})

	t.Run("a concrete hour reads its own row", func(t *testing.T) {
		hour := rankingHour
		found, _, _, err := repository.FindRanking(
			ctx,
			combinaciones.Query{Hour: &hour, Limit: 5},
		)
		if err != nil {
			t.Fatalf("FindRanking() error = %v", err)
		}
		if len(found) == 0 || found[0].EstimatedTrips != rankingVolume {
			t.Fatalf("hour %d did not return the fixture: %#v", hour, found)
		}
	})

	t.Run("an hour the fixture does not cover excludes it", func(t *testing.T) {
		hour := (rankingHour + 1) % combinaciones.HoursInDay
		found, _, _, err := repository.FindRanking(
			ctx,
			combinaciones.Query{Hour: &hour, Limit: 5},
		)
		if err != nil {
			t.Fatalf("FindRanking() error = %v", err)
		}
		for _, combination := range found {
			if combination.First.LineID == rankingFirstLineID {
				t.Fatal("the fixture appears in an hour it has no row for")
			}
		}
	})

	t.Run("the offset walks past the fixture", func(t *testing.T) {
		found, total, maximum, err := repository.FindRanking(
			ctx,
			combinaciones.Query{Hour: nil, Limit: 5, Offset: 1},
		)
		if err != nil {
			t.Fatalf("FindRanking() error = %v", err)
		}
		for _, combination := range found {
			if combination.First.LineID == rankingFirstLineID {
				t.Fatal("the first row is still there at offset 1")
			}
		}
		// The maximum has to keep describing the whole ranking even on a page
		// that no longer contains it, or the second page would paint its
		// severity against its own first row.
		if len(found) > 0 && maximum != rankingVolume {
			t.Errorf("maximum on page 2 = %v, want the ranking's %v", maximum, rankingVolume)
		}
		if len(found) > 0 && total < 2 {
			t.Errorf("total = %d, want at least 2", total)
		}
	})
}

func insertRankingFixture(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()

	execFixture(ctx, t, tx, `
		INSERT INTO vialis.paradas (id_parada, gtfs_stop_id, nombre, posicion)
		VALUES
			($1, 'ranking-bajada', 'Ranking bajada', ST_SetSRID(ST_MakePoint($3, $5), 4326)),
			($2, 'ranking-subida', 'Ranking subida', ST_SetSRID(ST_MakePoint($4, $5), 4326))`,
		rankingBoardStopID, rankingAlightStopID,
		rankingLongitude, rankingLongitude+0.0003, rankingLatitude,
	)

	execFixture(ctx, t, tx, `
		INSERT INTO vialis.recorridos (
			id_recorrido, gtfs_route_id, gtfs_shape_id, nombre_publico,
			linea, ramal, direction_id, geom, distancia_metros
		) VALUES
			($1, 'ranking-a', 'ranking-a', 'Ranking A', 'RA', 'TRONCAL', 0,
			 ST_SetSRID(ST_MakeLine(ST_MakePoint($3, $5), ST_MakePoint($4, $5)), 4326), 5000),
			($2, 'ranking-b', 'ranking-b', 'Ranking B', 'RB', 'TRONCAL', 1,
			 ST_SetSRID(ST_MakeLine(ST_MakePoint($4, $5), ST_MakePoint($3, $5)), 4326), 5000)`,
		rankingFirstLineID, rankingSecondLineID,
		rankingLongitude, rankingLongitude+0.05, rankingLatitude,
	)

	execFixture(ctx, t, tx, `
		INSERT INTO vialis.conexiones_recorridos (
			id_recorrido_origen, id_recorrido_destino,
			id_parada_bajada, id_parada_subida, distancia_caminata_metros
		) VALUES ($1, $2, $3, $4, 33)`,
		rankingFirstLineID, rankingSecondLineID,
		rankingBoardStopID, rankingAlightStopID,
	)

	execFixture(ctx, t, tx, `
		INSERT INTO vialis.hexagonos_viajes (indice_h3, punto_maxima_concurrencia, concurrencia)
		SELECT h3_lat_lng_to_cell(punto, 8), punto, 1
		FROM (VALUES
			(ST_SetSRID(ST_MakePoint($1, $3), 4326)),
			(ST_SetSRID(ST_MakePoint($2, $3), 4326))
		) AS v(punto)
		ON CONFLICT (indice_h3) DO NOTHING`,
		rankingLongitude, rankingLongitude+0.1, rankingLatitude,
	)

	// Both rows the aggregate stores for a pair: the concrete hour, and the
	// whole-day row whose NULL is a value and not a gap.
	execFixture(ctx, t, tx, `
		INSERT INTO vialis.combinaciones_lineas (
			id_recorrido_primero, id_recorrido_segundo, rango_horario,
			viajes_estimados, alternativas_promedio, rango_horario_pico
		) VALUES
			($1, $2, $3, $4, 2, $3),
			($1, $2, NULL, $4, 2, $3)`,
		rankingFirstLineID, rankingSecondLineID, rankingHour, rankingVolume,
	)

	execFixture(ctx, t, tx, `
		INSERT INTO vialis.combinaciones_lineas_flujos (
			id_recorrido_primero, id_recorrido_segundo, posicion,
			h3_origen, h3_destino, rango_horario, viajes_estimados, alternativas
		)
		SELECT $1, $2, 1,
			h3_lat_lng_to_cell(ST_SetSRID(ST_MakePoint($3, $5), 4326), 8),
			h3_lat_lng_to_cell(ST_SetSRID(ST_MakePoint($4, $5), 4326), 8),
			$6, $7, 2`,
		rankingFirstLineID, rankingSecondLineID,
		rankingLongitude, rankingLongitude+0.1, rankingLatitude,
		rankingHour, rankingVolume,
	)
}

func execFixture(ctx context.Context, t *testing.T, tx pgx.Tx, sql string, arguments ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, sql, arguments...); err != nil {
		t.Fatalf("insert fixture: %v", err)
	}
}
