package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	scripts "github.com/ManuelGarciaF/vialis-motor/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type bottlenecksStoreFixture struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	store *BottlenecksStore
	t     *testing.T
}

// Committed isolated schemas allow real independent connections/concurrent
// transactions. Cleanup drops only the unique test-owned schema, never vialis.
func newBottlenecksStoreFixture(t *testing.T) *bottlenecksStoreFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	schema := fmt.Sprintf("bottlenecks_test_%d", time.Now().UnixNano())
	if _, err = pool.Exec(ctx, `
		CREATE SCHEMA
	`+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if _, err := pool.Exec(cleanup, `
			DROP SCHEMA
		`+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("cleanup Bottlenecks schema: %v", err)
		}
	})
	s := &BottlenecksStore{database: pool, schema: schema}
	f := &bottlenecksStoreFixture{ctx: ctx, pool: pool, store: s, t: t}
	f.exec(`
		CREATE TABLE vialis.capturas_colectivos (
		    id_captura bigint PRIMARY KEY,
		    recibido_en timestamptz NOT NULL,
		    cobertura_parcial boolean NOT NULL DEFAULT false
		);

		CREATE TABLE vialis.recorridos (
		    id_recorrido bigint PRIMARY KEY,
		    gtfs_route_id text,
		    direction_id smallint,
		    gtfs_shape_id text,
		    linea text,
		    ramal text,
		    geom geometry(LineString, 4326)
		);

		CREATE TABLE vialis.paradas (
		    id_parada bigint PRIMARY KEY,
		    gtfs_stop_id text,
		    nombre text,
		    posicion geometry(Point, 4326)
		);

		CREATE TABLE vialis.recorridos_paradas (
		    id_recorrido bigint,
		    id_parada bigint,
		    nro_parada integer
		);

		INSERT INTO vialis.recorridos
		VALUES (
		    1,
		    'r',
		    0,
		    's',
		    'L',
		    'R',
		    ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.39 -34.6)', 4326)
		);

		INSERT INTO vialis.paradas
		VALUES (
		    1,
		    'stop-1',
		    'A',
		    ST_Point(- 58.41, - 34.6, 4326)
		),
		(
		    2,
		    'stop-2',
		    'B',
		    ST_Point(- 58.39, - 34.6, 4326)
		);

		INSERT INTO vialis.recorridos_paradas
		VALUES (
		    1,
		    1,
		    0
		),
		(
		    1,
		    2,
		    1
		);
	`)
	// Use the canonical DDL, without duplicating persistence definitions in tests.
	_, persistenceDDL, ok := strings.Cut(scripts.DDL, "-- Bottlenecks:")
	if !ok {
		t.Fatal("bottlenecks section missing from sql/ddl.sql")
	}
	f.exec("-- Bottlenecks:" + persistenceDDL)
	f.capture(5)
	f.capture(10)
	return f
}

func (f *bottlenecksStoreFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, f.store.sql(query), args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *bottlenecksStoreFixture) count(table string) int {
	f.t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, `
		SELECT count(*)
		FROM
	`+pgx.Identifier{f.store.schema, table}.Sanitize()).Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}

var passageEpoch = time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)

func (f *bottlenecksStoreFixture) capture(id int64) {
	f.exec(`
		INSERT INTO vialis.capturas_colectivos (
		    id_captura,
		    recibido_en
		)
		VALUES (
		    $1,
		    $2
		)
	`, id, passageEpoch.Add(time.Duration(id)*time.Minute))
}
func persistenceModel() bottlenecks.PersistenceModel {
	return bottlenecks.PersistenceModel{Name: "experimental", MeasurementVersion: "commercial-v1", MatcherVersion: "conservative-v1", NominalMeters: 500, Policy: bottlenecks.MeasurementPolicy{MaximumGap: 180 * time.Second, MaximumDistanceMeters: 100, MaximumSpeedKPH: 100}, TimeZone: "America/Argentina/Buenos_Aires", RawRetentionDays: 7, ClosureCaptures: 4}
}
func (f *bottlenecksStoreFixture) activate() (bottlenecks.ProcessingRun, bottlenecks.PersistedLayout) {
	f.t.Helper()
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		f.t.Fatal(err)
	}
	layout, err := f.store.RegisterLayout(f.ctx, run.Model.Name, 1)
	if err != nil {
		f.t.Fatal(err)
	}
	return run, layout
}
func TestBottlenecksStoreActivationSnapshotAndCatalogIndependence(t *testing.T) {
	f := newBottlenecksStoreFixture(t)
	run, layout := f.activate()
	if run.ActivationCaptureID != 10 || run.Checkpoint != 10 || f.count("bottlenecks_mediciones") != 0 {
		t.Fatal("activation backfilled data")
	}
	f.capture(11)
	again, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil || again.ActivationCaptureID != 10 || again.Checkpoint != 10 || !again.ActivatedAt.Equal(run.ActivatedAt) {
		t.Fatalf("activation reset: %+v %v", again, err)
	}
	changed := persistenceModel()
	changed.Policy.MaximumGap = time.Minute
	if _, err = f.store.Activate(f.ctx, changed); !errors.Is(err, bottlenecks.ErrModelChanged) {
		t.Fatalf("model mixed: %v", err)
	}
	same, err := f.store.RegisterLayout(f.ctx, run.Model.Name, 1)
	if err != nil || same.ID != layout.ID {
		t.Fatalf("duplicate layout: %v", err)
	}
	var sectors, stops int
	var delta float64
	err = f.pool.QueryRow(f.ctx, f.store.sql(`
		SELECT(SELECT count(*) FROM vialis.bottlenecks_sectores WHERE trazado = $1),
		jsonb_array_length(paradas),
		(SELECT max(abs(ST_Length(s.geom::geography) -(fin_metros - inicio_metros))) FROM vialis.bottlenecks_sectores s WHERE trazado = $1)
		FROM vialis.bottlenecks_trazados
		WHERE id = $1
	`), layout.ID).Scan(&sectors, &stops, &delta)
	if err != nil || sectors != len(layout.Layout.Sectors()) || stops != 2 || delta > 0.001 {
		t.Fatalf("snapshot geometry %d %d delta=%f: %v", sectors, stops, delta, err)
	}
	var first, last int
	err = f.pool.QueryRow(f.ctx, f.store.sql(`
		SELECT(paradas->0->'projectionCandidates'->0->>'sectorIndex')::int,
		(paradas->1->'projectionCandidates'->0->>'sectorIndex')::int
		FROM vialis.bottlenecks_trazados
		WHERE id = $1
	`), layout.ID).Scan(&first, &last)
	if err != nil || first != 0 || last != sectors-1 {
		t.Fatalf("stop/sector associations: %d %d %v", first, last, err)
	}
	f.exec(`
		UPDATE vialis.recorridos
		SET
		    geom = ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.38 -34.6)', 4326),
		    gtfs_shape_id = 'new'
		WHERE id_recorrido = 1
	`)
	replacement, err := f.store.RegisterLayout(f.ctx, run.Model.Name, 1)
	if err != nil || replacement.ID == layout.ID || replacement.Layout.Version() == layout.Layout.Version() {
		t.Fatalf("version overwritten: %v", err)
	}
	f.exec(`
		TRUNCATE vialis.recorridos_paradas,
		vialis.paradas,
		vialis.recorridos,
		vialis.capturas_colectivos
	`)
	if f.count("bottlenecks_trazados") != 2 || f.count("bottlenecks_sectores") <= sectors {
		t.Fatal("catalog/raw purge removed static evidence")
	}
	again, err = f.store.Activate(f.ctx, persistenceModel())
	if err != nil || again.Checkpoint != 10 {
		t.Fatalf("purge reset checkpoint: %+v %v", again, err)
	}
}
