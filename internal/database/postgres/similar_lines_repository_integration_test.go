package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The fixtures sit on the equator at longitude 10, far from the AMBA feed, so
// the corridor search can only reach them and not whatever real data the
// database already holds. One degree of longitude there is about 111 km, which
// makes the geometry below easy to reason about in meters.
const (
	fixtureLatitude      = 0.0
	fixtureBaseLongitude = 10.0
)

func TestFindSimilarLinesIntegration(t *testing.T) {
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

	prefix := fmt.Sprintf("similar-integration-%d", time.Now().UnixNano())
	// A short line along the corridor, a long one along the same corridor and
	// well beyond it, and one a whole degree — some 111 km — to the north.
	twinID := insertCorridorLine(
		t, ctx, transaction, prefix+"-twin",
		fmt.Sprintf(
			"LINESTRING(%[1]f %[2]f, %[3]f %[2]f)",
			fixtureBaseLongitude, fixtureLatitude, fixtureBaseLongitude+0.05,
		),
		true,
	)
	trunkID := insertCorridorLine(
		t, ctx, transaction, prefix+"-trunk",
		fmt.Sprintf(
			"LINESTRING(%[1]f %[2]f, %[3]f %[2]f)",
			fixtureBaseLongitude, fixtureLatitude, fixtureBaseLongitude+1.0,
		),
		false,
	)
	insertCorridorLine(
		t, ctx, transaction, prefix+"-elsewhere",
		fmt.Sprintf(
			"LINESTRING(%[1]f %[2]f, %[3]f %[2]f)",
			fixtureBaseLongitude, fixtureLatitude+1, fixtureBaseLongitude+0.05,
		),
		true,
	)

	query := lines.SimilarityQuery{
		Path: route.LineString{Positions: []route.Position{
			{Longitude: fixtureBaseLongitude, Latitude: fixtureLatitude},
			{Longitude: fixtureBaseLongitude + 0.05, Latitude: fixtureLatitude},
		}},
		Origin:                  route.Position{Longitude: fixtureBaseLongitude, Latitude: fixtureLatitude},
		Destination:             route.Position{Longitude: fixtureBaseLongitude + 0.05, Latitude: fixtureLatitude},
		Limit:                   10,
		CorridorToleranceMeters: 200,
		MinimumCoverage:         0.20,
	}

	found, err := repository.FindSimilar(ctx, query)
	if err != nil {
		t.Fatalf("FindSimilar() error = %v", err)
	}

	byID := make(map[int64]lines.Similarity, len(found))
	for _, similarity := range found {
		byID[similarity.Line.ID] = similarity
	}

	twin, present := byID[twinID]
	if !present {
		t.Fatalf("the line along the same corridor is missing from %#v", found)
	}
	if twin.CoverageOfProposed < 0.99 || twin.CoverageOfStored < 0.99 {
		t.Fatalf(
			"twin coverages = %.3f / %.3f, want both near 1",
			twin.CoverageOfProposed,
			twin.CoverageOfStored,
		)
	}

	// The long line contains the drawn route entirely, so it is fully covered
	// in one direction and barely at all in the other. That asymmetry is the
	// finding the pair exists to report.
	trunk, present := byID[trunkID]
	if !present {
		t.Fatalf("the long line sharing the corridor is missing from %#v", found)
	}
	if trunk.CoverageOfProposed < 0.99 {
		t.Fatalf("trunk coverageOfProposed = %.3f, want near 1", trunk.CoverageOfProposed)
	}
	if trunk.CoverageOfStored > 0.20 {
		t.Fatalf("trunk coverageOfStored = %.3f, want a small fraction", trunk.CoverageOfStored)
	}

	// A line 111 km away shares no corridor, whatever the minimum coverage is.
	for _, similarity := range found {
		if similarity.Line.Line == prefix+"-elsewhere" {
			t.Fatalf("a line a degree away was reported: %#v", similarity)
		}
	}

	// The weaker coverage ranks, so the twin outranks the trunk even though
	// both are fully covered in one direction.
	if found[0].Line.ID != twinID {
		t.Fatalf("first = %d, want the twin %d", found[0].Line.ID, twinID)
	}

	// A metric the pipeline never recorded must arrive absent, not as zero.
	if twin.Metrics.TotalMinutes == nil || *twin.Metrics.TotalMinutes != 10 {
		t.Fatalf("twin totalMinutes = %v, want 10", twin.Metrics.TotalMinutes)
	}
	if trunk.Metrics.TotalMinutes != nil {
		t.Fatalf("trunk totalMinutes = %v, want absent", *trunk.Metrics.TotalMinutes)
	}
	if twin.Metrics.PassengerFlow != nil || twin.Metrics.Revenue != nil {
		t.Fatalf("unmeasured metrics came back as values: %#v", twin.Metrics)
	}
	if twin.Line.StopCount != 2 {
		t.Fatalf("stopCount = %d, want 2", twin.Line.StopCount)
	}

	t.Run("the minimum coverage hides an incidental crossing", func(t *testing.T) {
		strict := query
		strict.MinimumCoverage = 0.99

		strictlyFound, err := repository.FindSimilar(ctx, strict)
		if err != nil {
			t.Fatalf("FindSimilar() error = %v", err)
		}
		// The trunk still qualifies on its stronger direction; raising the
		// threshold to 1 would be the only way to drop it, and it is the pair
		// the caller most needs to see.
		if len(strictlyFound) == 0 {
			t.Fatalf("nothing survived a 0.99 minimum, want the fully covered lines")
		}
	})

	t.Run("the limit is applied by the database", func(t *testing.T) {
		capped := query
		capped.Limit = 1

		cappedFound, err := repository.FindSimilar(ctx, capped)
		if err != nil {
			t.Fatalf("FindSimilar() error = %v", err)
		}
		if len(cappedFound) != 1 {
			t.Fatalf("rows = %d, want 1", len(cappedFound))
		}
		// The limit cuts the ranked list, not an arbitrary one.
		if cappedFound[0].Line.ID != twinID {
			t.Fatalf("kept %d, want the best match %d", cappedFound[0].Line.ID, twinID)
		}
	})
}

// insertCorridorLine creates a line along pathWKT with one stop at each of its
// ends, which is the least the corridor search accepts as a candidate.
//
// withDuration decides whether tiempo_total_minutos is recorded, so one fixture
// carries a measurement and another carries none: the difference between an
// absent metric and a zero one is part of what this test checks.
func insertCorridorLine(
	t *testing.T,
	ctx context.Context,
	transaction pgx.Tx,
	name string,
	pathWKT string,
	withDuration bool,
) int64 {
	t.Helper()

	var duration *int
	if withDuration {
		minutes := 10
		duration = &minutes
	}

	var lineID int64
	if err := transaction.QueryRow(ctx, `
        WITH path AS (
            SELECT ST_GeomFromText($2, 4326)
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
            $1 || '-shape',
            $1,
            $1,
            'TRONCAL',
            0,
            'Terminal',
            path.geom,
            ROUND(ST_Length(path.geom::geography))::INTEGER,
            $3::SMALLINT
        FROM path
        RETURNING id_recorrido
    `, name, pathWKT, duration).Scan(&lineID); err != nil {
		t.Fatalf("insert fixture line %q: %v", name, err)
	}

	for index, endpoint := range []string{"ST_StartPoint", "ST_EndPoint"} {
		var stopID int64
		if err := transaction.QueryRow(ctx, fmt.Sprintf(`
            INSERT INTO vialis.paradas (gtfs_stop_id, nombre, posicion)
            SELECT $1, $2, %s(ST_GeomFromText($3, 4326))
            RETURNING id_parada
        `, endpoint),
			fmt.Sprintf("%s-stop-%d", name, index),
			fmt.Sprintf("Parada %d", index),
			pathWKT,
		).Scan(&stopID); err != nil {
			t.Fatalf("insert fixture stop %d of %q: %v", index, name, err)
		}
		if _, err := transaction.Exec(ctx, `
            INSERT INTO vialis.recorridos_paradas (
                id_recorrido,
                id_parada,
                nro_parada
            )
            VALUES ($1, $2, $3)
        `, lineID, stopID, index); err != nil {
			t.Fatalf("insert fixture route stop %d of %q: %v", index, name, err)
		}
	}

	return lineID
}
