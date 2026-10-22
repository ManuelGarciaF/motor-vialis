package postgres

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type producerCountingBeginner struct {
	database                                         realtimeBeginner
	prepares, analyses, stateWrites, retentionChecks int
}

func (b *producerCountingBeginner) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := b.database.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &producerCountingTx{Tx: tx, counts: b}, nil
}

type producerCountingTx struct {
	pgx.Tx
	counts *producerCountingBeginner
}

func (tx *producerCountingTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if sql == prepareProducerProjectionSQL {
		tx.counts.prepares++
	}
	if strings.HasPrefix(strings.TrimSpace(sql), "ANALYZE pg_temp.bottlenecks_projection_edges") {
		tx.counts.analyses++
	}
	if strings.Contains(sql, "jsonb_array_elements") {
		tx.counts.stateWrites++
	}
	return tx.Tx.Exec(ctx, sql, args...)
}

func (tx *producerCountingTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if sql == checkBottleneckRetentionSQL {
		tx.counts.retentionChecks++
	}
	return tx.Tx.QueryRow(ctx, sql, args...)
}

func TestProducerStorePreparesOnceAndWritesStatesOnceForMultipleClosures(t *testing.T) {
	f := newProducerStoreFixture(t)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	f.capture(11)
	for _, vehicle := range []string{"a", "b", "c"} {
		f.producerRaw(11, vehicle, "g", 0, 250)
		f.producerRaw(11, vehicle, "g", 120, 1250)
	}
	for id := int64(11); id <= 14; id++ {
		if id != 11 {
			f.capture(id)
		}
		f.processProducer(run.Model.Name)
	}
	f.capture(15)
	counts := &producerCountingBeginner{database: f.pool}
	f.store.database = counts
	closed := f.processProducer(run.Model.Name)
	if closed.ClosedVehicles != 3 || closed.Passages != 3 || closed.Incomplete != 3 {
		t.Fatalf("closure evidence changed: %+v", closed)
	}
	if counts.prepares != 1 || counts.analyses != 1 || counts.stateWrites != 1 || counts.retentionChecks > 5 {
		t.Fatalf("per-unit database work returned: %+v", counts)
	}
	_, states := f.producerRecovery(run.Model.Name)
	for _, state := range states {
		if state.LastCaptureID != 15 || state.Open != nil {
			t.Fatalf("bulk state write lost closure: %+v", state)
		}
	}
}

func TestProducerStoreIndexedProjectionMatchesBatchAcrossGeometries(t *testing.T) {
	f := newProducerStoreFixture(t)
	f.exec(`
		UPDATE vialis.recorridos SET geom=ST_GeomFromText(
		 'LINESTRING(-58.41 -34.6,-58.40 -34.6,-58.40 -34.6,-58.40 -34.59,-58.41 -34.6,-58.42 -34.6)',4326);
		INSERT INTO vialis.recorridos(id_recorrido,gtfs_route_id,direction_id,gtfs_shape_id,linea,ramal,geom)
		VALUES(2,'other',1,'other-shape','Other','B',ST_GeomFromText(
		 'LINESTRING(179.99 60,-179.99 60,-179.99 60,-179.98 60)',4326));
		CREATE TABLE vialis.equivalencias_recorridos_gcba(gcba_route_id text,gcba_direction smallint,gtfs_route_id text,gtfs_direction_id smallint);
		INSERT INTO vialis.equivalencias_recorridos_gcba VALUES ('g1',0,'r',0),('g2',1,'other',1);
	`)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	layouts := make(map[int64]bottlenecks.PersistedLayout)
	for _, id := range []int64{1, 2} {
		layout, err := f.store.RegisterLayout(f.ctx, run.Model.Name, id)
		if err != nil {
			t.Fatal(err)
		}
		layouts[layout.ID] = layout
	}
	f.capture(11)
	f.exec(`
		INSERT INTO vialis.posiciones_colectivos_raw
		SELECT ($1::timestamptz AT TIME ZONE 'UTC')::date,$1::timestamptz+n*interval '1 second',$1,
		 'v-'||r.id_recorrido||'-'||n,'g'||r.id_recorrido,r.direction_id,
		 r.gtfs_route_id,r.direction_id,r.gtfs_shape_id,md5(encode(ST_AsEWKB(r.geom),'hex')),
		 ST_LineInterpolatePoint(r.geom::geography,n/10.0)::geometry,11
		FROM vialis.recorridos r CROSS JOIN generate_series(0,10) n;
	`, passageEpoch)
	f.exec(`UPDATE vialis.capturas_colectivos SET filas_insertadas=22 WHERE id_captura=11`)
	conn, err := f.pool.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	tx, err := conn.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackBottlenecks(tx)
	if err = f.store.prepareProducerProjection(f.ctx, tx, run, layouts); err != nil {
		t.Fatal(err)
	}
	actual, err := f.store.readProducerCandidates(f.ctx, tx, run, producerSourceBounds{first: 11, last: 11})
	if err != nil {
		t.Fatal(err)
	}
	expected := []bottlenecks.CandidateObservation{}
	repo := &BottlenecksRepository{schema: f.store.schema}
	for _, id := range []int64{1, 2} {
		data, err := repo.load(f.ctx, tx, id, bottlenecks.Window{From: passageEpoch, To: passageEpoch.Add(time.Minute)}, run.Model.Policy.MaximumDistanceMeters)
		if err != nil {
			t.Fatal(err)
		}
		expected = append(expected, data.Observations...)
	}
	sort.Slice(expected, func(i, j int) bool { return expected[i].VehicleID < expected[j].VehicleID })
	if len(actual) != len(expected) {
		t.Fatalf("projection population mismatch: %d / %d", len(actual), len(expected))
	}
	for i, row := range actual {
		if !reflect.DeepEqual(row.Candidate, expected[i]) {
			t.Fatalf("indexed projection changed alternatives at row %d: %+v / %+v", i, row.Candidate, expected[i])
		}
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	var dropped bool
	if err = conn.QueryRow(f.ctx, `SELECT to_regclass('pg_temp.bottlenecks_projection_edges') IS NULL`).Scan(&dropped); err != nil || !dropped {
		t.Fatalf("geometry cache survived commit: dropped=%v err=%v", dropped, err)
	}
}
