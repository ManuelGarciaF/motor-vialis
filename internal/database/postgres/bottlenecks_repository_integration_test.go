package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func bottleneckFixture(t *testing.T) *realtimeFixture {
	t.Helper()
	f := newRealtimeFixture(t, time.Minute)
	_, err := f.tx.Exec(f.ctx, f.sql(`
		CREATE TABLE vialis.recorridos (
		    id_recorrido bigint,
		    gtfs_route_id text,
		    direction_id smallint,
		    gtfs_shape_id text,
		    geom geometry(LineString, 4326),
		    linea text DEFAULT 'L',
		    ramal text DEFAULT 'R'
		);

		CREATE TABLE vialis.equivalencias_recorridos_gcba (
		    gcba_route_id text,
		    gcba_direction smallint,
		    gtfs_route_id text,
		    gtfs_direction_id smallint
		);

		INSERT INTO vialis.equivalencias_recorridos_gcba
		VALUES (
		    'g',
		    0,
		    'r',
		    0
		);

		CREATE TABLE vialis.posiciones_colectivos_raw (
		    dia_observado date,
		    observado_en timestamptz,
		    id_vehiculo text,
		    gcba_route_id text,
		    gcba_direction smallint,
		    gtfs_route_id text,
		    gtfs_direction_id smallint,
		    gtfs_shape_id text,
		    gtfs_shape_hash text,
		    posicion geometry(Point, 4326)
		);

		INSERT INTO vialis.recorridos (
		    id_recorrido,
		    gtfs_route_id,
		    direction_id,
		    gtfs_shape_id,
		    geom
		)
		VALUES (
		    1,
		    'r',
		    0,
		    's',
		    ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.39 -34.6)', 4326)
		);

		INSERT INTO vialis.posiciones_colectivos_raw
		SELECT
		    '2026-10-10',
		    '2026-10-10T12:00:00Z'::timestamptz + k * interval '60 seconds',
		    'v',
		    'g',
		    0,
		    'r',
		    0,
		    's',
		    md5(encode(ST_AsEWKB(geom), 'hex')),
		    ST_LineInterpolatePoint(geom, k / 4.0)
		FROM vialis.recorridos
		CROSS JOIN generate_series(0, 4) k;
	`))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestBottlenecksGeodesicProjection(t *testing.T) {
	f := bottleneckFixture(t)
	repo := &BottlenecksRepository{schema: f.schema}
	window := bottlenecks.Window{From: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 10, 12, 4, 0, 1000, time.UTC)}
	data, err := repo.load(f.ctx, f.tx, 1, window, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Observations) != 5 || data.LengthMeters < 1800 || data.LengthMeters > 1900 {
		t.Fatalf("unexpected dataset %+v", data)
	}
	for i, o := range data.Observations {
		if len(o.Candidates) != 1 || o.Candidates[0].DistanceMeters > 0.1 {
			t.Fatalf("projection %d: %+v", i, o)
		}
	}

	layout, err := bottlenecks.NewLayout(data.Version, data.LengthMeters, 500)
	if err != nil {
		t.Fatal(err)
	}
	policy := bottlenecks.MeasurementPolicy{MaximumGap: 3 * time.Minute, MaximumDistanceMeters: 100, MaximumSpeedKPH: 100}
	matched, err := bottlenecks.MatchConservatively(layout, data.Observations, policy)
	if err != nil {
		t.Fatal(err)
	}
	measured, err := bottlenecks.Measure(layout, matched.Observations, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(measured.Passages) != len(layout.Sectors()) {
		t.Fatalf("missing passage: %+v", measured)
	}
	window.To = window.From.Add(time.Minute)
	cut, err := repo.load(f.ctx, f.tx, 1, window, 100)
	if err != nil || len(cut.Observations) != 1 {
		t.Fatalf("not [from,to): %v %d", err, len(cut.Observations))
	}
	if _, err := repo.load(f.ctx, f.tx, 99, window, 100); !errors.Is(err, bottlenecks.ErrLineNotFound) {
		t.Fatalf("not-found: %v", err)
	}
}

func TestBottlenecksTargetsUseExplicitEquivalences(t *testing.T) {
	f := bottleneckFixture(t)
	_, err := f.tx.Exec(f.ctx, f.sql(`
		UPDATE vialis.equivalencias_recorridos_gcba
		SET gcba_direction = 1;

		UPDATE vialis.posiciones_colectivos_raw
		SET gcba_direction = 1;

		INSERT INTO vialis.recorridos (
		    id_recorrido,
		    gtfs_route_id,
		    direction_id,
		    gtfs_shape_id,
		    geom
		)
		VALUES (
		    2,
		    'other',
		    1,
		    'other-shape',
		    ST_GeomFromText('LINESTRING(-58.41 -34.7,-58.39 -34.7)', 4326)
		);

		INSERT INTO vialis.equivalencias_recorridos_gcba
		VALUES (
		    'r',
		    0,
		    'other',
		    1
		);

		INSERT INTO vialis.posiciones_colectivos_raw
		SELECT
		    '2026-10-10',
		    '2026-10-10T12:01:30Z',
		    'collision',
		    'r',
		    0,
		    'r',
		    0,
		    's',
		    md5(encode(ST_AsEWKB(geom), 'hex')),
		    ST_StartPoint(geom)
		FROM vialis.recorridos
		WHERE id_recorrido = 1;

		INSERT INTO vialis.posiciones_colectivos_raw
		SELECT
		    '2026-10-10',
		    '2026-10-10T12:01:30Z',
		    'unmapped',
		    'api-not-mapped',
		    0,
		    'r',
		    0,
		    's',
		    md5(encode(ST_AsEWKB(geom), 'hex')),
		    ST_StartPoint(geom)
		FROM vialis.recorridos
		WHERE id_recorrido = 1;
	`))
	if err != nil {
		t.Fatal(err)
	}
	repo := &BottlenecksRepository{schema: f.schema}
	window := bottlenecks.Window{From: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 10, 13, 0, 0, 0, time.UTC)}
	targets, err := repo.listTargets(f.ctx, func(ctx context.Context, sql string, args ...any) (rowIterator, error) {
		return f.tx.Query(ctx, sql, args...)
	}, window)
	if err != nil || len(targets) != 2 || targets[0].LineID != 1 || targets[0].ExpectedRawPositions != 5 || targets[1].ExpectedRawPositions != 1 {
		t.Fatalf("targets %+v %v", targets, err)
	}
	data, err := repo.load(f.ctx, f.tx, 1, window, 100)
	if err != nil || len(data.Observations) != 5 {
		t.Fatalf("namespace collision or unmapped row leaked: %+v %v", data, err)
	}
	for _, o := range data.Observations {
		if o.Scope.ProviderRouteID != "g" || o.Scope.ProviderDirection != 1 || o.Scope.Version.Direction != 0 {
			t.Fatalf("direction mapping %+v", o)
		}
	}

	historical, err := repo.load(f.ctx, f.tx, 2, window, 100)
	if err != nil || len(historical.Observations) != 1 || len(historical.Observations[0].Candidates) != 0 {
		t.Fatalf("historical target reprojected %+v %v", historical, err)
	}
}

func TestBottlenecksLoopKeepsAlternativesAndOldHashUnmatched(t *testing.T) {
	f := bottleneckFixture(t)
	_, err := f.tx.Exec(f.ctx, f.sql(`
		DELETE
		FROM vialis.posiciones_colectivos_raw;

		UPDATE vialis.recorridos
		SET
		    geom = ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.39 -34.6,-58.39 -34.59,-58.4 -34.6,-58.41 -34.6)',
		    4326);

		INSERT INTO vialis.posiciones_colectivos_raw
		SELECT
		    '2026-10-10',
		    '2026-10-10T12:00:00Z',
		    'v',
		    'g',
		    0,
		    'r',
		    0,
		    's',
		    md5(encode(ST_AsEWKB(geom), 'hex')),
		    ST_StartPoint(geom)
		FROM vialis.recorridos;

		INSERT INTO vialis.posiciones_colectivos_raw
		SELECT
		    '2026-10-10',
		    '2026-10-10T12:01:00Z',
		    'old',
		    'g',
		    0,
		    'r',
		    0,
		    's',
		    'old-hash',
		    ST_StartPoint(geom)
		FROM vialis.recorridos;

		INSERT INTO vialis.posiciones_colectivos_raw
		SELECT
		    '2026-10-10',
		    '2026-10-10T12:01:00Z',
		    'far',
		    'g',
		    0,
		    'r',
		    0,
		    's',
		    md5(encode(ST_AsEWKB(geom), 'hex')),
		    ST_Point(- 58.5, - 34.7, 4326)
		FROM vialis.recorridos;
	`))
	if err != nil {
		t.Fatal(err)
	}
	repo := &BottlenecksRepository{schema: f.schema}
	data, err := repo.load(f.ctx, f.tx, 1, bottlenecks.Window{From: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 10, 13, 0, 0, 0, time.UTC)}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Observations) != 3 {
		t.Fatalf("dropped raw: %d", len(data.Observations))
	}
	for _, o := range data.Observations {
		if o.VehicleID == "v" {
			if len(o.Candidates) < 2 {
				t.Fatalf("loop alternatives lost: %+v", o)
			}
		} else if len(o.Candidates) != 0 {
			t.Fatalf("old/far matched: %+v", o)
		}
	}
}
