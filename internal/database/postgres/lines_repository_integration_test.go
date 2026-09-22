package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLinesRepositoryIntegration(t *testing.T) {
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

	repository := newLinesRepository(func(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (rowIterator, error) {
		return transaction.Query(ctx, sql, arguments...)
	})

	// Keep the fixture outside AMBA to isolate it from existing feed data.
	fixturePrefix := fmt.Sprintf("lines-integration-%d", time.Now().UnixNano())
	lineID := insertStoredLine(t, ctx, transaction, fixturePrefix)

	t.Run("FindLine returns the stops in route order", func(t *testing.T) {
		stored, err := repository.FindLine(ctx, lineID)
		if err != nil {
			t.Fatalf("FindLine() error = %v", err)
		}
		if len(stored.Stops) != 3 {
			t.Fatalf("stops = %d, want 3", len(stored.Stops))
		}
		for index := 1; index < len(stored.Stops); index++ {
			if stored.Stops[index].StopNumber <= stored.Stops[index-1].StopNumber {
				t.Fatalf(
					"stops are not ordered by stop_sequence: %d then %d",
					stored.Stops[index-1].StopNumber,
					stored.Stops[index].StopNumber,
				)
			}
		}
		if stored.Summary.Line != "TEST" || stored.Summary.Branch != "TRONCAL" {
			t.Fatalf("summary = %#v, want the fixture metadata", stored.Summary)
		}
		if stored.Stops[0].PathToNext == nil ||
			len(stored.Stops[0].PathToNext.Positions) < 2 {
			t.Fatalf("stops[0].PathToNext = %#v, want a decoded LineString",
				stored.Stops[0].PathToNext)
		}
		if stored.Stops[2].PathToNext != nil {
			t.Fatalf("last stop path = %#v, want nil", stored.Stops[2].PathToNext)
		}
	})

	t.Run("FindLine reports an unknown id", func(t *testing.T) {
		_, err := repository.FindLine(ctx, -1)
		if !errors.Is(err, lines.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})

	t.Run("ListSummaries finds the fixture by search", func(t *testing.T) {
		summaries, total, err := repository.ListSummaries(ctx, lines.Query{
			Search: fixturePrefix,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if total != 1 || len(summaries) != 1 {
			t.Fatalf("total/rows = %d/%d, want 1/1", total, len(summaries))
		}
		summary := summaries[0]
		if summary.ID != lineID {
			t.Fatalf("id = %d, want %d", summary.ID, lineID)
		}
		if summary.StopCount != 3 {
			t.Fatalf("stopCount = %d, want 3", summary.StopCount)
		}
	})

	t.Run("ListSummaries filters by bounding box", func(t *testing.T) {
		inside := lines.Bounds{
			MinLongitude: -50.1,
			MinLatitude:  -40.1,
			MaxLongitude: -49.9,
			MaxLatitude:  -39.9,
		}
		summaries, _, err := repository.ListSummaries(ctx, lines.Query{
			Search: fixturePrefix,
			Bounds: &inside,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if len(summaries) != 1 {
			t.Fatalf("rows inside the box = %d, want 1", len(summaries))
		}

		elsewhere := lines.Bounds{
			MinLongitude: 10,
			MinLatitude:  10,
			MaxLongitude: 11,
			MaxLatitude:  11,
		}
		summaries, total, err := repository.ListSummaries(ctx, lines.Query{
			Search: fixturePrefix,
			Bounds: &elsewhere,
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if len(summaries) != 0 || total != 0 {
			t.Fatalf("rows outside the box = %d (total %d), want none", len(summaries), total)
		}
	})

	// Search metacharacters must be treated as literal data.
	t.Run("ListSummaries escapes search wildcards", func(t *testing.T) {
		summaries, _, err := repository.ListSummaries(ctx, lines.Query{
			Search: "lines_integration",
			Limit:  10,
		})
		if err != nil {
			t.Fatalf("ListSummaries() error = %v", err)
		}
		if len(summaries) != 0 {
			t.Fatalf("rows = %d, want none: the underscore must not act as a wildcard",
				len(summaries))
		}
	})
}

// insertStoredLine creates a valid three-stop ETL fixture.
func insertStoredLine(
	t *testing.T,
	ctx context.Context,
	transaction pgx.Tx,
	prefix string,
) int64 {
	t.Helper()

	const pathWKT = "LINESTRING(-50 -40, -49.99 -40, -49.98 -40)"

	var lineID int64
	if err := transaction.QueryRow(ctx, `
        WITH path AS (
            SELECT ST_GeomFromText($3, 4326)
                ::GEOMETRY(LineString, 4326) AS geom
        )
        INSERT INTO vialis.recorridos (
            gtfs_route_id,
            gtfs_shape_id,
            nombre_publico,
            linea,
            ramal,
            direction_id,
            destino,
            geom,
            distancia_metros,
            tiempo_total_minutos
        )
        SELECT
            $1,
            $2,
            $1,
            'TEST',
            'TRONCAL',
            0,
            'Terminal',
            path.geom,
            ROUND(ST_Length(path.geom::geography))::INTEGER,
            10
        FROM path
        RETURNING id_recorrido
    `, prefix, prefix+"-shape", pathWKT).Scan(&lineID); err != nil {
		t.Fatalf("insert fixture line: %v", err)
	}

	longitudes := []float64{-50, -49.99, -49.98}
	stopIDs := make([]int64, len(longitudes))
	for index, longitude := range longitudes {
		gtfsStopID := fmt.Sprintf("%s-stop-%d", prefix, index)
		if err := transaction.QueryRow(ctx, `
            INSERT INTO vialis.paradas (gtfs_stop_id, codigo, nombre, posicion)
            VALUES ($1, $2, $3, ST_SetSRID(ST_MakePoint($4, -40), 4326))
            RETURNING id_parada
        `, gtfsStopID, fmt.Sprintf("C%d", index), fmt.Sprintf("Parada %d", index),
			longitude).Scan(&stopIDs[index]); err != nil {
			t.Fatalf("insert fixture stop %d: %v", index, err)
		}
	}

	// GTFS stop_sequence values need not be contiguous.
	stopNumbers := []int{5, 15, 25}
	segments := []any{
		"LINESTRING(-50 -40, -49.99 -40)",
		"LINESTRING(-49.99 -40, -49.98 -40)",
		nil,
	}
	for index := range stopIDs {
		if _, err := transaction.Exec(ctx, `
            INSERT INTO vialis.recorridos_paradas (
                id_recorrido,
                id_parada,
                nro_parada,
                tramo_hasta_siguiente,
                distancia_hasta_siguiente_metros
            )
            VALUES (
                $1,
                $2,
                $3,
                CASE
                    WHEN $4::TEXT IS NOT NULL
                    THEN ST_GeomFromText($4::TEXT, 4326)
                        ::GEOMETRY(LineString, 4326)
                END,
                CASE
                    WHEN $4::TEXT IS NOT NULL
                    THEN ROUND(
                        ST_Length(ST_GeomFromText($4::TEXT, 4326)::geography)
                    )::INTEGER
                END
            )
        `, lineID, stopIDs[index], stopNumbers[index], segments[index]); err != nil {
			t.Fatalf("insert fixture route stop %d: %v", index, err)
		}
	}

	return lineID
}
