package postgres

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func newProducerStoreFixture(t *testing.T) *bottlenecksStoreFixture {
	t.Helper()
	f := newBottlenecksStoreFixture(t)
	f.exec(`ALTER TABLE vialis.capturas_colectivos ADD COLUMN filas_insertadas bigint NOT NULL DEFAULT 0;
 CREATE TABLE vialis.posiciones_colectivos_raw(
 dia_observado date NOT NULL,observado_en timestamptz NOT NULL,recibido_en timestamptz NOT NULL,
 id_vehiculo text NOT NULL,gcba_route_id text NOT NULL,gcba_direction smallint NOT NULL,
 gtfs_route_id text NOT NULL,gtfs_direction_id smallint NOT NULL,gtfs_shape_id text NOT NULL,
 gtfs_shape_hash text NOT NULL,posicion geometry(Point,4326) NOT NULL,id_captura bigint NOT NULL,
 PRIMARY KEY(dia_observado,id_vehiculo,observado_en));`)
	return f
}

func (f *bottlenecksStoreFixture) producerRaw(capture int64, vehicle, provider string, seconds int, progress float64) {
	f.t.Helper()
	f.exec(`INSERT INTO vialis.posiciones_colectivos_raw
 SELECT ($1::timestamptz AT TIME ZONE 'UTC')::date,$1,$2,$3,$4,0,
 gtfs_route_id,direction_id,gtfs_shape_id,md5(encode(ST_AsEWKB(geom),'hex')),
 ST_LineInterpolatePoint(geom::geography,$5/ST_Length(geom::geography))::geometry,$6
 FROM vialis.recorridos WHERE id_recorrido=1`, passageEpoch.Add(time.Duration(seconds)*time.Second), passageEpoch.Add(time.Duration(capture)*time.Minute), vehicle, provider, progress, capture)
	f.exec(`UPDATE vialis.capturas_colectivos SET filas_insertadas=filas_insertadas+1 WHERE id_captura=$1`, capture)
}

func (f *bottlenecksStoreFixture) processProducer(model string) bottlenecks.ProducerProgress {
	f.t.Helper()
	p, err := f.store.ProcessNext(f.ctx, model)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f *bottlenecksStoreFixture) producerRecovery(model string) (bottlenecks.ProcessingRun, []bottlenecks.ProducerState) {
	f.t.Helper()
	run, states, err := f.store.LoadProducerStates(f.ctx, model)
	if err != nil {
		f.t.Fatal(err)
	}
	return run, states
}

func TestProducerStoreActivationRecoveryFinalReplayAndLateInsertion(t *testing.T) {
	f := newProducerStoreFixture(t)
	f.producerRaw(5, "historical", "g", -60, 0)
	if ready, err := f.store.ProducerSchemaReady(f.ctx); err != nil || !ready {
		t.Fatalf("producer schema readiness: %v %v", ready, err)
	}
	if _, _, err := f.store.LoadProducerStates(f.ctx, "not-activated"); !errors.Is(err, bottlenecks.ErrModelNotFound) {
		t.Fatalf("recovery implicitly activated a missing model: %v", err)
	}
	if _, err := f.store.ProcessNext(f.ctx, "not-activated"); !errors.Is(err, bottlenecks.ErrModelNotFound) {
		t.Fatalf("processing implicitly activated a missing model: %v", err)
	}
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	recovered, states := f.producerRecovery(run.Model.Name)
	if recovered.Checkpoint != 10 || len(states) != 0 {
		t.Fatal("recovery backfilled or changed activation checkpoint")
	}
	idle := f.processProducer(run.Model.Name)
	if idle.Processed || idle.Checkpoint != 10 {
		t.Fatalf("idle progressed: %+v", idle)
	}
	f.capture(11)
	f.producerRaw(11, "v", "g", 0, 250)
	first := f.processProducer(run.Model.Name)
	if first.Passages != 0 || f.count("bottlenecks_mediciones") != 0 {
		t.Fatal("published OPEN prefix")
	}
	f.capture(12)
	f.producerRaw(12, "v", "g", 120, 1250)
	f.processProducer(run.Model.Name)
	f.capture(13)
	f.producerRaw(13, "v", "g", 60, 750)
	f.processProducer(run.Model.Name)
	recovered, states = f.producerRecovery(run.Model.Name)
	if recovered.Checkpoint != 13 || len(states) != 1 || states[0].Open.Observations != 3 || states[0].MissingCaptures != 1 {
		t.Fatalf("late evidence lost: %+v", states)
	}
	f.capture(14)
	f.exec(`UPDATE vialis.capturas_colectivos SET cobertura_parcial=true WHERE id_captura=14`)
	f.processProducer(run.Model.Name)
	for id := int64(15); id <= 16; id++ {
		f.capture(id)
		p := f.processProducer(run.Model.Name)
		if p.Passages != 0 || p.ClosedVehicles != 0 {
			t.Fatal("closed early")
		}
	}
	f.capture(17)
	closed := f.processProducer(run.Model.Name)
	if closed.ClosedVehicles != 1 || closed.Passages != 1 || closed.Incomplete != 1 {
		t.Fatalf("closure: %+v", closed)
	}
	_, states = f.producerRecovery(run.Model.Name)
	if states[0].Open != nil || states[0].MissingCaptures != 4 {
		t.Fatal("closure state not recovered")
	}
	var data []byte
	if err = f.pool.QueryRow(f.ctx, f.store.sql(`SELECT soporte FROM vialis.bottlenecks_mediciones WHERE completa`)).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var passage bottlenecks.Passage
	if err = json.Unmarshal(data, &passage); err != nil {
		t.Fatal(err)
	}
	if passage.InteriorObservations != 1 || passage.SameGPSInterval || passage.Duration < 59*time.Second || passage.Duration > 61*time.Second {
		t.Fatalf("late sample not used in final measurement: %+v", passage)
	}
	before := f.count("bottlenecks_mediciones")
	idle = f.processProducer(run.Model.Name)
	if idle.Processed || f.count("bottlenecks_mediciones") != before {
		t.Fatal("idle/retry duplicated closure")
	}
	// Later reception of older GPS is diagnosed, not attached to closed evidence.
	f.capture(18)
	f.producerRaw(18, "v", "g", 90, 900)
	late := f.processProducer(run.Model.Name)
	if late.LateObservations != 1 || late.ClosedVehicles != 0 || f.count("bottlenecks_mediciones") != before {
		t.Fatalf("reopened: %+v", late)
	}
}

func TestProducerStoreScopeBarriersPreserveEventTimeABA(t *testing.T) {
	f := newProducerStoreFixture(t)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	f.capture(11)
	f.producerRaw(11, "v", "A", 0, 250)
	f.producerRaw(11, "v", "A", 60, 750)
	f.producerRaw(11, "v", "A", 120, 1250)
	f.processProducer(run.Model.Name)
	// B arrives late between the two A pieces. Arrival order cannot finalize A.
	f.capture(12)
	f.producerRaw(12, "v", "B", 90, 850)
	f.processProducer(run.Model.Name)
	for id := int64(13); id <= 15; id++ {
		f.capture(id)
		p := f.processProducer(run.Model.Name)
		if id == 15 && (p.ClosedVehicles != 1 || p.Passages != 0 || p.Incomplete != 1) {
			t.Fatalf("A-B-A bridged: %+v", p)
		}
	}
	var reason string
	if err = f.pool.QueryRow(f.ctx, f.store.sql(`SELECT motivo FROM vialis.bottlenecks_mediciones`)).Scan(&reason); err != nil || reason != "scope_changed" {
		t.Fatalf("scope terminal reason %q: %v", reason, err)
	}
}

func TestProducerStorePartialAndStationaryNewerTimestamp(t *testing.T) {
	f := newProducerStoreFixture(t)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	f.capture(11)
	f.producerRaw(11, "v", "g", 0, 250)
	f.processProducer(run.Model.Name)
	for id := int64(12); id <= 14; id++ {
		f.capture(id)
		f.processProducer(run.Model.Name)
	}
	f.capture(15)
	f.exec(`UPDATE vialis.capturas_colectivos SET cobertura_parcial=true WHERE id_captura=15`)
	f.producerRaw(15, "v", "g", 60, 250)
	p := f.processProducer(run.Model.Name)
	_, states := f.producerRecovery(run.Model.Name)
	if p.ClosedVehicles != 0 || states[0].MissingCaptures != 0 || states[0].Open.Observations != 2 {
		t.Fatal("partial stationary update counted absent")
	}
	f.capture(16)
	f.exec(`UPDATE vialis.capturas_colectivos SET cobertura_parcial=true WHERE id_captura=16`)
	f.processProducer(run.Model.Name)
	_, states = f.producerRecovery(run.Model.Name)
	if states[0].MissingCaptures != 0 {
		t.Fatal("partial absence counted")
	}
}

func TestProducerStoreSourceLossAndExpiredOpenDoNotAdvance(t *testing.T) {
	f := newProducerStoreFixture(t)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	f.capture(11)
	f.producerRaw(11, "v", "g", 0, 250)
	f.exec(`DELETE FROM vialis.posiciones_colectivos_raw WHERE id_captura=11`)
	if _, err = f.store.ProcessNext(f.ctx, run.Model.Name); !errors.Is(err, bottlenecks.ErrProducerSourceIncomplete) {
		t.Fatalf("missing source looked empty: %v", err)
	}
	recovered, _ := f.producerRecovery(run.Model.Name)
	if recovered.Checkpoint != 10 || f.count("bottlenecks_estados_productor") != 0 {
		t.Fatal("source loss advanced checkpoint")
	}
	f.exec(`UPDATE vialis.capturas_colectivos SET filas_insertadas=0 WHERE id_captura=11`)
	f.producerRaw(11, "v", "g", 0, 250)
	f.processProducer(run.Model.Name)
	_, states := f.producerRecovery(run.Model.Name)
	state := states[0]
	state.Open.EarliestAt = passageEpoch.AddDate(0, 0, -8)
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE vialis.bottlenecks_estados_productor SET estado=$2 WHERE modelo=$1`, run.Model.Name, data)
	f.capture(12)
	if _, err = f.store.ProcessNext(f.ctx, run.Model.Name); !errors.Is(err, bottlenecks.ErrSourceExpired) {
		t.Fatalf("old OPEN raw ignored behind fresh run: %v", err)
	}
	recovered, states = f.producerRecovery(run.Model.Name)
	if recovered.Checkpoint != 11 || states[0].LastCaptureID != 11 {
		t.Fatal("expired context advanced")
	}
}

func TestProducerStoreMissingExactGeometryAndMultiVehicleRollback(t *testing.T) {
	f := newProducerStoreFixture(t)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	f.capture(11)
	f.producerRaw(11, "a", "g", 0, 250)
	f.producerRaw(11, "z", "g", 0, 250)
	f.exec(`UPDATE vialis.posiciones_colectivos_raw SET gtfs_shape_hash='unavailable' WHERE id_vehiculo='z'`)
	if _, err = f.store.ProcessNext(f.ctx, run.Model.Name); !errors.Is(err, bottlenecks.ErrProducerGeometryUnavailable) {
		t.Fatalf("substituted geometry: %v", err)
	}
	if f.count("bottlenecks_trazados") != 0 || f.count("bottlenecks_estados_productor") != 0 {
		t.Fatal("partial snapshots/state escaped")
	}
	f.exec(`UPDATE vialis.posiciones_colectivos_raw SET gtfs_shape_hash=(SELECT md5(encode(ST_AsEWKB(geom),'hex')) FROM vialis.recorridos WHERE id_recorrido=1)`)
	f.processProducer(run.Model.Name)
	_, before := f.producerRecovery(run.Model.Name)
	// Force a SQL failure after vehicle a writes, before z can be stored.
	f.exec(`ALTER TABLE vialis.bottlenecks_estados_productor ADD CONSTRAINT reject_z CHECK (vehiculo<>'z' OR checkpoint<12)`)
	f.capture(12)
	if _, err = f.store.ProcessNext(f.ctx, run.Model.Name); err == nil {
		t.Fatal("SQL failure accepted")
	}
	recovered, after := f.producerRecovery(run.Model.Name)
	if recovered.Checkpoint != 11 || !reflect.DeepEqual(before, after) {
		t.Fatal("partial multi-vehicle commit escaped")
	}
}

func TestProducerStoreConcurrentWriters(t *testing.T) {
	f := newProducerStoreFixture(t)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	f.capture(11)
	f.producerRaw(11, "v", "g", 0, 250)
	type outcome struct {
		p   bottlenecks.ProducerProgress
		err error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := f.store.ProcessNext(f.ctx, run.Model.Name)
			results <- outcome{p, err}
		}()
	}
	wg.Wait()
	close(results)
	processed := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.p.Processed {
			processed++
		}
	}
	recovered, states := f.producerRecovery(run.Model.Name)
	if processed != 1 || recovered.Checkpoint != 11 || len(states) != 1 || states[0].Open.Observations != 1 {
		t.Fatal("duplicate concurrent processing or inconsistent recovery")
	}
}

func TestProducerStoreReplayFingerprintAndSnapshotIndependence(t *testing.T) {
	f := newProducerStoreFixture(t)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	f.capture(11)
	f.producerRaw(11, "v", "g", 0, 250)
	f.producerRaw(11, "v", "g", 120, 1250)
	f.processProducer(run.Model.Name)
	for id := int64(12); id <= 14; id++ {
		f.capture(id)
		f.processProducer(run.Model.Name)
	}
	f.capture(15)
	f.exec(`UPDATE vialis.posiciones_colectivos_raw SET posicion=ST_Translate(posicion,0.001,0) WHERE id_captura=11`)
	if _, err = f.store.ProcessNext(f.ctx, run.Model.Name); !errors.Is(err, bottlenecks.ErrProducerSourceIncomplete) {
		t.Fatalf("changed replay not classified as incomplete source: %v", err)
	}
	recovered, states := f.producerRecovery(run.Model.Name)
	if recovered.Checkpoint != 14 || states[0].MissingCaptures != 3 {
		t.Fatal("failed replay progressed")
	}
	// Restore identical canonical coordinates then remove live catalog entirely.
	f.exec(`DELETE FROM vialis.posiciones_colectivos_raw WHERE id_captura=11`)
	f.exec(`UPDATE vialis.capturas_colectivos SET filas_insertadas=0 WHERE id_captura=11`)
	f.producerRaw(11, "v", "g", 0, 250)
	f.producerRaw(11, "v", "g", 120, 1250)
	f.exec(`TRUNCATE vialis.recorridos,vialis.recorridos_paradas,vialis.paradas`)
	closed := f.processProducer(run.Model.Name)
	if closed.Passages != 1 {
		t.Fatalf("saved shape replay depended on live catalog: %+v", closed)
	}
}
