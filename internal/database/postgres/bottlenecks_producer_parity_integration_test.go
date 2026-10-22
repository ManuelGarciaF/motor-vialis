package postgres

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func TestProducerStoreLoopProjectionAndClosedBatchParity(t *testing.T) {
	f := newProducerStoreFixture(t)
	f.exec(`UPDATE vialis.recorridos SET geom=ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.40 -34.6,-58.40 -34.59,-58.41 -34.6,-58.42 -34.6)',4326);
 CREATE TABLE vialis.equivalencias_recorridos_gcba(gcba_route_id text,gcba_direction smallint,gtfs_route_id text,gtfs_direction_id smallint);
 INSERT INTO vialis.equivalencias_recorridos_gcba VALUES('g',0,'r',0);`)
	run, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil {
		t.Fatal(err)
	}
	f.capture(11)
	f.producerRaw(11, "v", "g", 0, 0)
	f.processProducer(run.Model.Name)
	f.capture(12)
	f.producerRaw(12, "v", "g", 60, 250)
	f.producerRaw(12, "v", "g", 180, 1250)
	f.processProducer(run.Model.Name)
	snapshot, err := f.store.RegisterLayout(f.ctx, run.Model.Name, 1)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	repo := &BottlenecksRepository{schema: f.store.schema}
	dataset, err := repo.load(f.ctx, tx, 1, bottlenecks.Window{From: passageEpoch.Add(-time.Second), To: passageEpoch.Add(181 * time.Second)}, run.Model.Policy.MaximumDistanceMeters)
	if err != nil {
		rollbackBottlenecks(tx)
		t.Fatal(err)
	}
	if err = f.store.prepareProducerProjection(f.ctx, tx, run, map[int64]bottlenecks.PersistedLayout{snapshot.ID: snapshot}); err != nil {
		rollbackBottlenecks(tx)
		t.Fatal(err)
	}
	producerRows, err := f.store.readProducerCandidates(f.ctx, tx, run, producerSourceBounds{first: 11, last: 12})
	rollbackBottlenecks(tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(producerRows) != len(dataset.Observations) {
		t.Fatal("projection population mismatch")
	}
	for i, row := range producerRows {
		if !reflect.DeepEqual(row.Candidate, dataset.Observations[i]) {
			t.Fatalf("snapshot projection differs from batch at %d: %+v / %+v", i, row.Candidate, dataset.Observations[i])
		}
	}
	layout, err := bottlenecks.NewLayout(dataset.Version, dataset.LengthMeters, run.Model.NominalMeters)
	if err != nil {
		t.Fatal(err)
	}
	// The producer canonicalizes event instants to UTC before hashing/replay.
	for i := range dataset.Observations {
		dataset.Observations[i].ObservedAt = dataset.Observations[i].ObservedAt.UTC()
	}
	matched, err := bottlenecks.MatchConservatively(layout, dataset.Observations, run.Model.Policy)
	if err != nil {
		t.Fatal(err)
	}
	if matched.TemporallyResolved == 0 {
		t.Fatalf("fixture has no backward-resolved loop ambiguity: %+v", matched)
	}
	expected, err := bottlenecks.Measure(layout, matched.Observations, run.Model.Policy)
	if err != nil {
		t.Fatal(err)
	}
	for id := int64(13); id <= 16; id++ {
		f.capture(id)
		result := f.processProducer(run.Model.Name)
		if id == 16 && (result.ClosedVehicles != 1 || result.TemporallyResolved != matched.TemporallyResolved || result.Passages != len(expected.Passages)) {
			t.Fatalf("closure/batch mismatch: %+v / %+v", result, expected)
		}
	}
	rows, err := f.pool.Query(f.ctx, f.store.sql(`SELECT soporte FROM vialis.bottlenecks_mediciones WHERE completa ORDER BY entrada_en,sector`))
	if err != nil {
		t.Fatal(err)
	}
	actual := []bottlenecks.Passage{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			break
		}
		var p bottlenecks.Passage
		if err = json.Unmarshal(data, &p); err != nil {
			break
		}
		actual = append(actual, p)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if rowErr != nil {
		t.Fatal(rowErr)
	}
	if !reflect.DeepEqual(actual, expected.Passages) {
		t.Fatalf("persisted closed evidence differs from batch: %+v / %+v", actual, expected.Passages)
	}
}
