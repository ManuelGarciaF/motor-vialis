package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	scripts "github.com/ManuelGarciaF/vialis-motor/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type passageStoreFixture struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	store *BottlenecksStore
	t     *testing.T
}

// Committed isolated schemas allow real independent connections/concurrent
// transactions. Cleanup drops only the unique test-owned schema, never vialis.
func newPassageStoreFixture(t *testing.T) *passageStoreFixture {
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
	schema := fmt.Sprintf("h0012_test_%d", time.Now().UnixNano())
	if _, err = pool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("cleanup H0012 schema: %v", err)
		}
	})
	s := &BottlenecksStore{database: pool, schema: schema}
	f := &passageStoreFixture{ctx: ctx, pool: pool, store: s, t: t}
	f.exec(`CREATE TABLE vialis.capturas_colectivos(id_captura bigint PRIMARY KEY,recibido_en timestamptz NOT NULL,cobertura_parcial boolean NOT NULL DEFAULT false);
 CREATE TABLE vialis.recorridos(id_recorrido bigint PRIMARY KEY,gtfs_route_id text,direction_id smallint,gtfs_shape_id text,linea text,ramal text,geom geometry(LineString,4326));
 CREATE TABLE vialis.paradas(id_parada bigint PRIMARY KEY,gtfs_stop_id text,nombre text,posicion geometry(Point,4326));
 CREATE TABLE vialis.recorridos_paradas(id_recorrido bigint,id_parada bigint,nro_parada integer);
 INSERT INTO vialis.recorridos VALUES(1,'r',0,'s','L','R',ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.39 -34.6)',4326));
 INSERT INTO vialis.paradas VALUES(1,'stop-1','A',ST_Point(-58.41,-34.6,4326)),(2,'stop-2','B',ST_Point(-58.39,-34.6,4326));
 INSERT INTO vialis.recorridos_paradas VALUES(1,1,0),(1,2,1);`)
	f.exec(scripts.CrearBottlenecks)
	f.exec(scripts.CrearBottlenecks)
	f.capture(5)
	f.capture(10)
	return f
}

func (f *passageStoreFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, f.store.sql(query), args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *passageStoreFixture) count(table string) int {
	f.t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FROM `+pgx.Identifier{f.store.schema, table}.Sanitize()).Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}

var passageEpoch = time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)

func (f *passageStoreFixture) capture(id int64) {
	f.exec(`INSERT INTO vialis.capturas_colectivos(id_captura,recibido_en) VALUES($1,$2)`, id, passageEpoch.Add(time.Duration(id)*time.Minute))
}
func persistenceModel() bottlenecks.PersistenceModel {
	return bottlenecks.PersistenceModel{Name: "experimental", MeasurementVersion: "commercial-v1", MatcherVersion: "conservative-v1", NominalMeters: 500, Policy: bottlenecks.MeasurementPolicy{MaximumGap: 180 * time.Second, MaximumDistanceMeters: 100, MaximumSpeedKPH: 100}, TimeZone: "America/Argentina/Buenos_Aires", RawRetentionDays: 7, ClosureCaptures: 4}
}
func (f *passageStoreFixture) activate() (bottlenecks.ProcessingRun, bottlenecks.PersistedLayout) {
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
func persistedObservation(layout bottlenecks.Layout, vehicle string, seconds int, progress float64) bottlenecks.Observation {
	return bottlenecks.Observation{VehicleID: vehicle, Scope: bottlenecks.Scope{ProviderRouteID: "g", Version: layout.Version()}, ObservedAt: passageEpoch.Add(time.Duration(seconds) * time.Second), Match: bottlenecks.Matched, ProgressMeters: progress}
}
func (f *passageStoreFixture) transition(layout bottlenecks.PersistedLayout, old *bottlenecks.CaptureState, id int64, rows ...bottlenecks.Observation) bottlenecks.StreamTransition {
	f.t.Helper()
	var state bottlenecks.CaptureState
	var previous int64
	var err error
	if old == nil {
		if len(rows) == 0 {
			f.t.Fatal("new state needs a vehicle")
		}
		state, err = bottlenecks.NewCaptureState(layout.Layout, persistenceModel().Policy, rows[0].VehicleID, 10)
		if err != nil {
			f.t.Fatal(err)
		}
	} else {
		state = *old
		previous = old.LastCaptureID
	}
	next, m, err := bottlenecks.MeasureCapture(layout.Layout, state, bottlenecks.CaptureInput{ID: id, ReceivedAt: passageEpoch.Add(time.Duration(id) * time.Minute), Successful: true, Observations: rows})
	if err != nil {
		f.t.Fatal(err)
	}
	return bottlenecks.StreamTransition{LayoutID: layout.ID, PreviousCaptureID: previous, State: next, Measurement: m}
}

func TestPassageStoreActivationSnapshotAndCatalogIndependence(t *testing.T) {
	f := newPassageStoreFixture(t)
	run, layout := f.activate()
	if run.ActivationCaptureID != 10 || run.Checkpoint != 10 || f.count("h0012_estados") != 0 || f.count("h0012_mediciones") != 0 {
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
	err = f.pool.QueryRow(f.ctx, f.store.sql(`SELECT (SELECT count(*) FROM vialis.h0012_sectores WHERE trazado=$1),jsonb_array_length(paradas),
 (SELECT max(abs(ST_Length(s.geom::geography)-(fin_metros-inicio_metros))) FROM vialis.h0012_sectores s WHERE trazado=$1)
 FROM vialis.h0012_trazados WHERE id=$1`), layout.ID).Scan(&sectors, &stops, &delta)
	if err != nil || sectors != len(layout.Layout.Sectors()) || stops != 2 || delta > 0.001 {
		t.Fatalf("snapshot geometry %d %d delta=%f: %v", sectors, stops, delta, err)
	}
	var first, last int
	err = f.pool.QueryRow(f.ctx, f.store.sql(`SELECT (paradas->0->'projectionCandidates'->0->>'sectorIndex')::int,(paradas->1->'projectionCandidates'->0->>'sectorIndex')::int FROM vialis.h0012_trazados WHERE id=$1`), layout.ID).Scan(&first, &last)
	if err != nil || first != 0 || last != sectors-1 {
		t.Fatalf("stop/sector associations: %d %d %v", first, last, err)
	}
	f.exec(`UPDATE vialis.recorridos SET geom=ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.38 -34.6)',4326),gtfs_shape_id='new' WHERE id_recorrido=1`)
	replacement, err := f.store.RegisterLayout(f.ctx, run.Model.Name, 1)
	if err != nil || replacement.ID == layout.ID || replacement.Layout.Version() == layout.Layout.Version() {
		t.Fatalf("version overwritten: %v", err)
	}
	f.exec(`TRUNCATE vialis.recorridos_paradas,vialis.paradas,vialis.recorridos,vialis.capturas_colectivos`)
	if f.count("h0012_trazados") != 2 || f.count("h0012_sectores") <= sectors {
		t.Fatal("catalog/raw purge removed static evidence")
	}
	again, err = f.store.Activate(f.ctx, persistenceModel())
	if err != nil || again.Checkpoint != 10 {
		t.Fatalf("purge reset checkpoint: %+v %v", again, err)
	}
}

func TestPassageStoreAtomicCommitRestartAndReplay(t *testing.T) {
	f := newPassageStoreFixture(t)
	run, layout := f.activate()
	f.capture(11)
	f.capture(12)
	first := f.transition(layout, nil, 11, persistedObservation(layout.Layout, "v", 0, 250), persistedObservation(layout.Layout, "v", 60, 750))
	result, err := f.store.CommitCapture(f.ctx, run.Model.Name, 10, 11, []bottlenecks.StreamTransition{first})
	if err != nil || result.Passages != 0 {
		t.Fatalf("first commit: %+v %v", result, err)
	}
	states, err := f.store.LoadStates(f.ctx, run.Model.Name)
	if err != nil || len(states) != 1 || !reflect.DeepEqual(states[0].State, first.State) {
		t.Fatalf("restart state mismatch: %+v %v", states, err)
	}
	second := f.transition(layout, &states[0].State, 12, persistedObservation(layout.Layout, "v", 120, 1250))
	result, err = f.store.CommitCapture(f.ctx, run.Model.Name, 11, 12, []bottlenecks.StreamTransition{second})
	if err != nil || result.Passages != 1 || f.count("h0012_mediciones") != 1 || f.count("h0012_diagnosticos") != 2 {
		t.Fatalf("passage commit: %+v %v", result, err)
	}
	for _, id := range []int64{11, 12} {
		if _, err = f.store.CommitCapture(f.ctx, run.Model.Name, 10, id, nil); !errors.Is(err, bottlenecks.ErrCaptureAlreadyProcessed) {
			t.Fatalf("replay classification: %v", err)
		}
	}
	if f.count("h0012_mediciones") != 1 || f.count("h0012_diagnosticos") != 2 {
		t.Fatal("replay duplicated evidence")
	}
	var payload []byte
	var date string
	var hour int
	if err = f.pool.QueryRow(f.ctx, f.store.sql(`SELECT soporte,fecha_local::text,hora_local FROM vialis.h0012_mediciones`)).Scan(&payload, &date, &hour); err != nil {
		t.Fatal(err)
	}
	var passage bottlenecks.Passage
	if err = json.Unmarshal(payload, &passage); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(passage, second.Measurement.Passages[0]) || date != passageEpoch.Format("2006-01-02") || hour != 9 {
		t.Fatalf("support/attribution changed: %+v %s %d", passage, date, hour)
	}
	f.exec(`TRUNCATE vialis.capturas_colectivos`)
	states, err = f.store.LoadStates(f.ctx, run.Model.Name)
	if err != nil || len(states) != 1 || states[0].State.LastCaptureID != 12 || f.count("h0012_mediciones") != 1 {
		t.Fatalf("raw purge damaged restart/evidence: %v", err)
	}
}

func TestPassageStoreFailureRollsBackResultsStatesAndCheckpoint(t *testing.T) {
	f := newPassageStoreFixture(t)
	run, layout := f.activate()
	f.capture(11)
	f.capture(12)
	first := f.transition(layout, nil, 11, persistedObservation(layout.Layout, "v", 0, 250), persistedObservation(layout.Layout, "v", 60, 750))
	if _, err := f.store.CommitCapture(f.ctx, run.Model.Name, 10, 11, []bottlenecks.StreamTransition{first}); err != nil {
		t.Fatal(err)
	}
	second := f.transition(layout, &first.State, 12, persistedObservation(layout.Layout, "v", 120, 1250))
	bad := second
	bad.State = first.State
	bad.State.VehicleID = "bad"
	bad.State.LastCaptureID = 999
	// The valid first transition inserts a passage before the second fails.
	if _, err := f.store.CommitCapture(f.ctx, run.Model.Name, 11, 12, []bottlenecks.StreamTransition{second, bad}); err == nil {
		t.Fatal("invalid second transition committed")
	}
	if f.count("h0012_mediciones") != 0 || f.count("h0012_diagnosticos") != 1 {
		t.Fatal("partial results escaped rollback")
	}
	states, err := f.store.LoadStates(f.ctx, run.Model.Name)
	if err != nil || !reflect.DeepEqual(states[0].State, first.State) {
		t.Fatalf("partial state escaped: %v", err)
	}
	unchanged, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil || unchanged.Checkpoint != 11 {
		t.Fatalf("checkpoint advanced on failure: %+v %v", unchanged, err)
	}
	if _, err = f.store.CommitCapture(f.ctx, run.Model.Name, 11, 12, nil); err == nil {
		t.Fatal("omitted stream accepted")
	}
	// A SQL constraint/unique failure after writes must roll back equally.
	duplicated := second
	duplicated.Measurement.Passages = append(append([]bottlenecks.Passage(nil), second.Measurement.Passages...), second.Measurement.Passages[0])
	if _, err = f.store.CommitCapture(f.ctx, run.Model.Name, 11, 12, []bottlenecks.StreamTransition{duplicated}); err == nil {
		t.Fatal("duplicate measurement identity accepted")
	}
	if f.count("h0012_mediciones") != 0 {
		t.Fatal("SQL failure left partial passage")
	}
	if _, err = f.store.CommitCapture(f.ctx, run.Model.Name, 11, 12, []bottlenecks.StreamTransition{second}); err != nil {
		t.Fatal(err)
	}
}

func TestPassageStoreCheckpointOrderingAndConcurrentReplay(t *testing.T) {
	f := newPassageStoreFixture(t)
	run, layout := f.activate()
	f.capture(11)
	f.capture(12)
	if _, err := f.store.CommitCapture(f.ctx, run.Model.Name, 10, 12, nil); !errors.Is(err, bottlenecks.ErrCaptureOutOfOrder) {
		t.Fatalf("skipped capture: %v", err)
	}
	if _, err := f.store.CommitCapture(f.ctx, run.Model.Name, 9, 11, nil); !errors.Is(err, bottlenecks.ErrCheckpointConflict) {
		t.Fatalf("stale checkpoint: %v", err)
	}
	first := f.transition(layout, nil, 11, persistedObservation(layout.Layout, "v", 0, 250), persistedObservation(layout.Layout, "v", 120, 1250))
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.store.CommitCapture(f.ctx, run.Model.Name, 10, 11, []bottlenecks.StreamTransition{first})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, replay := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, bottlenecks.ErrCaptureAlreadyProcessed) {
			replay++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || replay != 1 || f.count("h0012_mediciones") != 1 || f.count("h0012_diagnosticos") != 1 {
		t.Fatalf("concurrent duplication: success=%d replay=%d", success, replay)
	}
}

func TestPassageStorePersistsFinalIncompleteClosure(t *testing.T) {
	f := newPassageStoreFixture(t)
	run, layout := f.activate()
	f.capture(11)
	transition := f.transition(layout, nil, 11, persistedObservation(layout.Layout, "v", 0, 250), persistedObservation(layout.Layout, "v", 60, 750))
	if _, err := f.store.CommitCapture(f.ctx, run.Model.Name, 10, 11, []bottlenecks.StreamTransition{transition}); err != nil {
		t.Fatal(err)
	}
	for id := int64(12); id <= 16; id++ {
		f.capture(id)
		next := f.transition(layout, &transition.State, id)
		result, err := f.store.CommitCapture(f.ctx, run.Model.Name, id-1, id, []bottlenecks.StreamTransition{next})
		if err != nil {
			t.Fatal(err)
		}
		if id == 15 && (result.Incomplete != 1 || result.ClosedStreams != 1) {
			t.Fatalf("closure: %+v", result)
		}
		transition = next
	}
	if f.count("h0012_mediciones") != 1 {
		t.Fatal("repeated incomplete closure")
	}
	var reason string
	var complete bool
	if err := f.pool.QueryRow(f.ctx, f.store.sql(`SELECT motivo,completa FROM vialis.h0012_mediciones`)).Scan(&reason, &complete); err != nil || reason != "missing_updates" || complete {
		t.Fatalf("invalid incomplete measurement: %s %v %v", reason, complete, err)
	}
}
