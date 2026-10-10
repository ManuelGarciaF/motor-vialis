package postgres

import (
	"os"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/jackc/pgx/v5"
)

// Optional realistic-volume check: one actual mapped snapshot is copied only
// into a rolled-back schema, then a synthetic advancing frame confirms pairs.
// This is not a reconstruction of historical real events.
func TestBunchingSnapshotVolume(t *testing.T) {
	if os.Getenv("REALTIME_BUNCHING_VOLUME_TEST") != "1" {
		t.Skip("requires TEST_DATABASE_URL and REALTIME_BUNCHING_VOLUME_TEST=1")
	}
	fixture := newRealtimeFixture(t, 2*time.Minute)
	fixture.migrate("migrar_realtime.sql")
	ctx, tx, schema := fixture.ctx, fixture.tx, fixture.schema
	repo := fixture.realtimeRepository()
	for _, table := range []string{"recorridos", "equivalencias_recorridos_gcba"} {
		if _, err := tx.Exec(ctx, "CREATE TABLE "+pgx.Identifier{schema, table}.Sanitize()+" AS SELECT * FROM "+pgx.Identifier{"vialis", table}.Sanitize()); err != nil {
			t.Fatal(err)
		}
	}
	policy := bunching.Policy{DistanceMeters: 200, MaximumAge: 5 * time.Minute, MaximumFutureSkew: time.Minute, MissingCaptureTolerance: 1, MinimumObservations: 2}
	if err := repo.EnableBunching(policy); err != nil {
		t.Fatal(err)
	}
	var capture bunching.Capture
	var id int64
	if err := tx.QueryRow(ctx, `SELECT id_captura,recibido_en FROM vialis.capturas_colectivos WHERE filas_insertadas>3000 ORDER BY id_captura LIMIT 1`).Scan(&id, &capture.ReceivedAt); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `SELECT id_vehiculo,gcba_route_id,gcba_direction,ST_X(posicion),ST_Y(posicion),observado_en,velocidad_metros_segundo FROM vialis.posiciones_colectivos_raw WHERE id_captura=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p bunching.Position
		if err = rows.Scan(&p.VehicleID, &p.ProviderRouteID, &p.ProviderDirection, &p.Longitude, &p.Latitude, &p.ObservedAt, &p.SpeedMetersPerSecond); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		capture.Positions = append(capture.Positions, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	capture.Stats = bunching.CaptureStats{TotalRows: len(capture.Positions), ValidRows: len(capture.Positions)}
	started := time.Now()
	first, err := repo.StoreCapture(ctx, capture)
	if err != nil {
		t.Fatal(err)
	}
	firstDuration := time.Since(started)
	if first.EventsOpened == 0 {
		t.Fatal("no candidate pairs in sample")
	}
	capture.ReceivedAt = capture.ReceivedAt.Add(time.Minute)
	for i := range capture.Positions {
		capture.Positions[i].ObservedAt = capture.Positions[i].ObservedAt.Add(time.Minute)
	}
	started = time.Now()
	second, err := repo.StoreCapture(ctx, capture)
	if err != nil {
		t.Fatal(err)
	}
	if second.EventsOpened != 0 || second.EventsUpdated != first.EventsOpened || second.EventsClosed != 0 {
		t.Fatalf("confirmation counters: first=%+v second=%+v", first, second)
	}
	t.Logf("positions=%d candidates=%d first=%s confirmation=%s", len(capture.Positions), first.EventsOpened, firstDuration, time.Since(started))
	reader := fixture.bunchingRepository()
	query := bunching.HistoryRequest{From: capture.ReceivedAt.Add(-10 * time.Minute), To: capture.ReceivedAt.Add(time.Second), State: "all", Limit: 50}
	started = time.Now()
	page, err := reader.FindHistory(ctx, query, capture.ReceivedAt, policy)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != int64(first.EventsOpened) {
		t.Fatalf("total: %d want %d", page.Total, first.EventsOpened)
	}
	t.Logf("global page: total=%d page=%d query=%s", page.Total, len(page.Events), time.Since(started))
}
