package postgres

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/jackc/pgx/v5"
)

func TestBunchingSpeedBackfillIntegration(t *testing.T) {
	fixture := newRealtimeFixture(t, time.Minute)
	fixture.migrate("migrar_realtime.sql", "migrar_aglutinaciones.sql")
	ctx, tx := fixture.ctx, fixture.tx
	repo := fixture.realtimeRepository()
	start := time.Date(2026, 10, 8, 23, 50, 0, 0, time.UTC)
	end := start.Add(20 * time.Minute)
	if _, err := tx.Exec(ctx, repo.sql(`CREATE TABLE vialis.posiciones_colectivos_raw_test PARTITION OF vialis.posiciones_colectivos_raw FOR VALUES FROM ('2026-10-08') TO ('2026-10-10')`)); err != nil {
		t.Fatal(err)
	}
	batch := &pgx.Batch{}
	for _, name := range []string{"moving", "stationary", "missing"} {
		e := bunching.Event{Scope: testBackfillScope(), VehicleA: name + "-a", VehicleB: name + "-b", FirstObservedAt: start, LastObservedAt: end, ObservedA: end.Add(-time.Minute), ObservedB: end, ConfirmedAt: end, ClosedAt: &end, CloseReason: "missing", PointA: bunching.Point{Longitude: -58.4, Latitude: -34.6}, PointB: bunching.Point{Longitude: -58.4005, Latitude: -34.6}, Observations: 16}
		if name == "moving" {
			e.MaximumSpeedAMetersPerSecond = 5
		}
		queueEvent(batch, repo, e)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	add := func(vehicle string, at, received time.Time, speed float64, scope bunching.Scope, partial, analyzed bool) {
		t.Helper()
		var captureID int64
		if err := tx.QueryRow(ctx, repo.sql(`INSERT INTO vialis.capturas_colectivos(recibido_en,filas_totales,filas_validas,filas_invalidas,filas_duplicadas,filas_stale,cobertura_parcial,aglutinaciones_analizadas) VALUES($1,1,1,0,0,0,$2,$3) RETURNING id_captura`), received, partial, analyzed).Scan(&captureID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, repo.sql(`INSERT INTO vialis.posiciones_colectivos_raw(dia_observado,observado_en,recibido_en,id_vehiculo,gcba_route_id,gcba_direction,gtfs_route_id,gtfs_direction_id,gtfs_shape_id,gtfs_shape_hash,posicion,velocidad_metros_segundo,id_captura) VALUES(($1::timestamptz AT TIME ZONE 'UTC')::date,$1,$2,$3,$4,$5,$6,$7,$8,$9,ST_SetSRID(ST_MakePoint(-58.4,-34.6),4326),$10,$11)`), at, received, vehicle, scope.GCBARouteID, scope.GCBADirection, scope.GTFSRouteID, scope.GTFSDirection, scope.ShapeID, scope.ShapeHash, speed, captureID); err != nil {
			t.Fatal(err)
		}
	}
	scope := testBackfillScope()
	add("moving-a", start.Add(2*time.Minute), start.Add(2*time.Minute), 3, scope, false, true)
	add("moving-b", end, end, 1, scope, false, true)
	// Exclude samples before the encounter, after each individual last GPS,
	// partial/unanalysed captures, stale GPS, excessive future skew and wrong scope.
	add("moving-a", start.Add(-time.Minute), start.Add(-time.Minute), 90, scope, false, true)
	add("moving-a", end.Add(-30*time.Second), end.Add(-30*time.Second), 90, scope, false, true)
	add("moving-b", end.Add(time.Minute), end.Add(time.Minute), 90, scope, false, true)
	add("moving-a", start.Add(3*time.Minute), start.Add(3*time.Minute), 90, scope, true, true)
	add("moving-a", start.Add(4*time.Minute), start.Add(4*time.Minute), 90, scope, false, false)
	add("moving-a", start.Add(5*time.Minute), start.Add(11*time.Minute), 90, scope, false, true)
	add("moving-a", start.Add(6*time.Minute), start.Add(4*time.Minute), 90, scope, false, true)
	wrong := scope
	wrong.GCBARouteID = "other"
	add("moving-a", start.Add(7*time.Minute), start.Add(7*time.Minute), 90, wrong, false, true)
	wrong = scope
	wrong.ShapeHash = "other"
	add("moving-a", start.Add(8*time.Minute), start.Add(8*time.Minute), 90, wrong, false, true)
	add("stationary-a", start.Add(time.Minute), start.Add(time.Minute), 0, scope, false, true)
	add("stationary-b", start.Add(time.Minute), start.Add(time.Minute), 0, scope, false, true)
	data, err := os.ReadFile("../../../scripts/backfill-bunching-speeds.sql")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	// Execute the actual SQL blocks without psql control commands, inside the
	// rolled-back fixture transaction. No production lock or COMMIT is issued.
	selectStart := strings.Index(script, "CREATE TEMP TABLE bunching_speed_backfill")
	selectEnd := strings.Index(script, "-- Los criterios de publicación")
	updateStart := strings.Index(script, "    WITH updated AS (")
	updateEnd := strings.Index(script, "    COMMIT;")
	if selectStart < 0 || selectEnd < selectStart || updateStart < 0 || updateEnd < updateStart {
		t.Fatal("missing backfill SQL blocks")
	}
	if _, err := tx.Exec(ctx, repo.sql(script[selectStart:selectEnd])); err != nil {
		t.Fatal(err)
	}
	var samplesA, samplesB int64
	if err := tx.QueryRow(ctx, `SELECT samples_a,samples_b FROM bunching_speed_backfill WHERE before_a=5`).Scan(&samplesA, &samplesB); err != nil || samplesA != 1 || samplesB != 1 {
		t.Fatalf("raw selection: %d %d %v", samplesA, samplesB, err)
	}
	updateSQL := repo.sql(script[updateStart:updateEnd])
	var updated int64
	if err := tx.QueryRow(ctx, updateSQL).Scan(&updated); err != nil || updated != 1 {
		t.Fatalf("updated: %d %v", updated, err)
	}
	var maxA, maxB float64
	if err := tx.QueryRow(ctx, repo.sql(`SELECT velocidad_maxima_a_metros_segundo,velocidad_maxima_b_metros_segundo FROM vialis.aglutinaciones_colectivos WHERE id_vehiculo_a='moving-a'`)).Scan(&maxA, &maxB); err != nil || maxA != 5 || maxB != 1 {
		t.Fatalf("maxima: %v %v %v", maxA, maxB, err)
	}
	if err := tx.QueryRow(ctx, updateSQL).Scan(&updated); err != nil || updated != 0 {
		t.Fatalf("non-idempotent update: %d %v", updated, err)
	}
	var unchanged int
	if err := tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.aglutinaciones_colectivos WHERE id_vehiculo_a!='moving-a' AND velocidad_maxima_a_metros_segundo=0 AND velocidad_maxima_b_metros_segundo=0 AND observaciones=16 AND primero_observado_en=$1 AND ultimo_observado_en=$2 AND cerrado_en=$2`), start, end).Scan(&unchanged); err != nil || unchanged != 2 {
		t.Fatalf("other events/lifecycle changed: %d %v", unchanged, err)
	}
}

func testBackfillScope() bunching.Scope {
	return bunching.Scope{GCBARouteID: "gcba", GTFSRouteID: "gtfs", ShapeID: "shape", ShapeHash: "hash"}
}
