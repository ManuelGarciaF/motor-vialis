package postgres

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func TestBottlenecksStorePreservesNanosecondIdentityAndSupport(t *testing.T) {
	f := newBottlenecksStoreFixture(t)
	run, layout := f.activate()
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackBottlenecks(tx)

	sector := layout.Layout.Sectors()[0]
	scope := bottlenecks.Scope{ProviderRouteID: "g", Version: layout.Layout.Version()}
	for ns := 1; ns <= 2; ns++ {
		entry := passageEpoch.Add(time.Duration(ns))
		exit := entry.Add(time.Minute)
		p := bottlenecks.Passage{VehicleID: "v", Scope: scope, Sector: sector, Entered: bottlenecks.Crossing{At: entry, BeforeAt: entry, AfterAt: entry}, Exited: bottlenecks.Crossing{At: exit, BeforeAt: exit, AfterAt: exit}, Duration: time.Minute, SpeedKPH: sector.LengthMeters() / 60 * 3.6}
		record := bottleneckMeasurementRecord{
			layoutID: layout.ID, captureID: 11,
			vehicle: p.VehicleID, scope: p.Scope, sector: p.Sector, entry: p.Entered,
			complete: true, duration: p.Duration, speedKPH: p.SpeedKPH, payload: p,
		}
		if err = f.store.insertMeasurement(f.ctx, tx, run, record); err != nil {
			t.Fatal(err)
		}
		var data []byte
		if err = tx.QueryRow(f.ctx, f.store.sql(`
			SELECT soporte
			FROM vialis.bottlenecks_mediciones
			ORDER BY identidad
			OFFSET $1
			LIMIT 1
		`), 0).Scan(&data); err != nil {
			t.Fatal(err)
		}
		var restored bottlenecks.Passage
		if err = json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if ns == 1 && !reflect.DeepEqual(restored, p) {
			t.Fatal("nanosecond support lost")
		}
	}

	var identities, timestamps int
	if err = tx.QueryRow(f.ctx, f.store.sql(`
		SELECT
		    count(DISTINCT identidad),
		    count(DISTINCT entrada_en)
		FROM vialis.bottlenecks_mediciones
	`)).Scan(&identities, &timestamps); err != nil {
		t.Fatal(err)
	}
	if identities != 2 || timestamps != 1 {
		t.Fatalf("submicrosecond identities collapsed: %d vs %d", identities, timestamps)
	}
}

func TestBottlenecksStoreStopSnapshotPreservesLoopAlternatives(t *testing.T) {
	f := newBottlenecksStoreFixture(t)
	f.exec(`
		UPDATE vialis.recorridos
		SET
		    geom = ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.40 -34.6,-58.40 -34.59,-58.41 -34.6,-58.42 -34.6)',
		    4326)
		WHERE id_recorrido = 1
	`)
	_, layout := f.activate()
	var count int
	if err := f.pool.QueryRow(f.ctx, f.store.sql(`
		SELECT jsonb_array_length(paradas->0->'projectionCandidates')
		FROM vialis.bottlenecks_trazados
		WHERE id = $1
	`), layout.ID).Scan(&count); err != nil || count < 2 {
		t.Fatalf("loop stop forced onto a single branch: %d %v", count, err)
	}
}
