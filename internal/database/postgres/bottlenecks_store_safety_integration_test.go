package postgres

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func TestPassageStoreRefusesExpiredSourceWithoutResettingActivation(t *testing.T) {
	f := newPassageStoreFixture(t)
	run, _ := f.activate()
	f.capture(11)
	f.exec(`
		UPDATE vialis.h0012_modelos
		SET activado_en = clock_timestamp() - interval '8 days'
	`)
	if _, err := f.store.CommitCapture(f.ctx, run.Model.Name, 10, 11, nil); !errors.Is(err, bottlenecks.ErrSourceExpired) {
		t.Fatalf("silently skipped purged history: %v", err)
	}
	again, err := f.store.Activate(f.ctx, persistenceModel())
	if err != nil || again.Checkpoint != 10 || !again.ActivatedAt.Before(run.ActivatedAt) {
		t.Fatalf("activation silently recovered gap: %+v %v", again, err)
	}
	f.exec(`
		UPDATE vialis.h0012_modelos
		SET activado_en = clock_timestamp()
	`)
	f.exec(`
		UPDATE vialis.capturas_colectivos
		SET recibido_en = clock_timestamp() - interval '8 days'
		WHERE id_captura = 11
	`)
	if _, err = f.store.CommitCapture(f.ctx, run.Model.Name, 10, 11, nil); !errors.Is(err, bottlenecks.ErrSourceExpired) {
		t.Fatalf("expired capture accepted: %v", err)
	}
	if f.count("h0012_diagnosticos") != 0 || f.count("h0012_mediciones") != 0 {
		t.Fatal("source gap emitted partial evidence")
	}
}

func TestPassageStoreUsesCapturePartialMetadata(t *testing.T) {
	f := newPassageStoreFixture(t)
	run, layout := f.activate()
	f.capture(11)
	f.capture(12)
	first := f.transition(layout, nil, 11, persistedObservation(layout.Layout, "v", 0, 250), persistedObservation(layout.Layout, "v", 60, 750))
	if _, err := f.store.CommitCapture(f.ctx, run.Model.Name, 10, 11, []bottlenecks.StreamTransition{first}); err != nil {
		t.Fatal(err)
	}
	f.exec(`
		UPDATE vialis.capturas_colectivos
		SET cobertura_parcial = true
		WHERE id_captura = 12
	`)
	incorrectlyCounted := f.transition(layout, &first.State, 12)
	if _, err := f.store.CommitCapture(f.ctx, run.Model.Name, 11, 12, []bottlenecks.StreamTransition{incorrectlyCounted}); err == nil {
		t.Fatal("producer overrode source partial status")
	}
	next, m, err := bottlenecks.MeasureCapture(layout.Layout, first.State, bottlenecks.CaptureInput{ID: 12, ReceivedAt: passageEpoch.Add(12 * time.Minute), Successful: true, Partial: true})
	if err != nil {
		t.Fatal(err)
	}
	correct := bottlenecks.StreamTransition{LayoutID: layout.ID, PreviousCaptureID: 11, State: next, Measurement: m}
	if _, err = f.store.CommitCapture(f.ctx, run.Model.Name, 11, 12, []bottlenecks.StreamTransition{correct}); err != nil {
		t.Fatal(err)
	}
	states, err := f.store.LoadStates(f.ctx, run.Model.Name)
	if err != nil || states[0].State.MissingCaptures != 0 {
		t.Fatalf("partial absence persisted: %+v %v", states, err)
	}
}

func TestPassageStoreRejectsMixedLayoutsModelsAndTechnicalCensoring(t *testing.T) {
	f := newPassageStoreFixture(t)
	run, layout := f.activate()
	f.capture(11)
	first := f.transition(layout, nil, 11, persistedObservation(layout.Layout, "v", 0, 250), persistedObservation(layout.Layout, "v", 120, 1250))
	for _, tt := range []struct {
		name   string
		mutate func(*bottlenecks.StreamTransition)
	}{
		{"layout", func(tr *bottlenecks.StreamTransition) { tr.LayoutID = 999999 }},
		{"policy", func(tr *bottlenecks.StreamTransition) { tr.State.Policy.MaximumSpeedKPH = 90 }},
		{"previous checkpoint", func(tr *bottlenecks.StreamTransition) { tr.PreviousCaptureID = 9 }},
		{"fake new absence", func(tr *bottlenecks.StreamTransition) { tr.State.MissingCaptures = 1 }},
		{"support", func(tr *bottlenecks.StreamTransition) { tr.Measurement.Passages[0].MaximumSupportingGap = 0 }},
		{"technical censoring", func(tr *bottlenecks.StreamTransition) {
			tr.Measurement.Incomplete = []bottlenecks.IncompletePassage{{VehicleID: "v", Scope: first.Measurement.Passages[0].Scope, Sector: first.Measurement.Passages[0].Sector, Entered: first.Measurement.Passages[0].Entered, Reason: bottlenecks.BatchBoundary}}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// JSON isolation avoids sharing mutable passage slices between cases.
			data, err := json.Marshal(first)
			if err != nil {
				t.Fatal(err)
			}
			var transition bottlenecks.StreamTransition
			if err = json.Unmarshal(data, &transition); err != nil {
				t.Fatal(err)
			}
			tt.mutate(&transition)
			if _, err = f.store.CommitCapture(f.ctx, run.Model.Name, 10, 11, []bottlenecks.StreamTransition{transition}); err == nil {
				t.Fatal("invalid transition accepted")
			}
			if f.count("h0012_mediciones") != 0 || f.count("h0012_estados") != 0 || f.count("h0012_diagnosticos") != 0 {
				t.Fatal("invalid transition left writes")
			}
		})
	}

	other := persistenceModel()
	other.Name = "other"
	if _, err := f.store.Activate(f.ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CommitCapture(f.ctx, other.Name, 10, 11, []bottlenecks.StreamTransition{first}); err == nil {
		t.Fatal("layout from another model accepted")
	}
}

func TestPassageStorePreservesNanosecondIdentityAndSupport(t *testing.T) {
	f := newPassageStoreFixture(t)
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
			FROM vialis.h0012_mediciones
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
		FROM vialis.h0012_mediciones
	`)).Scan(&identities, &timestamps); err != nil {
		t.Fatal(err)
	}
	if identities != 2 || timestamps != 1 {
		t.Fatalf("submicrosecond identities collapsed: %d vs %d", identities, timestamps)
	}
}

func TestPassageStoreStopSnapshotPreservesLoopAlternatives(t *testing.T) {
	f := newPassageStoreFixture(t)
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
		FROM vialis.h0012_trazados
		WHERE id = $1
	`), layout.ID).Scan(&count); err != nil || count < 2 {
		t.Fatalf("loop stop forced onto a single branch: %d %v", count, err)
	}
}
