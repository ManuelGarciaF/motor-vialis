package postgres

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/realtime"
	"github.com/ManuelGarciaF/vialis-motor/internal/transport/gcba"
)

type stoppingRealtimeRepository struct {
	*RealtimeRepository
	cancel context.CancelFunc
	stores int
}

func (r *stoppingRealtimeRepository) StoreCapture(ctx context.Context, c bunching.Capture) (bunching.StoreResult, error) {
	result, err := r.RealtimeRepository.StoreCapture(ctx, c)
	if err == nil {
		r.stores++
		if r.stores == 2 {
			r.cancel()
		}
	}
	return result, err
}

// Exercises HTTP decoding -> worker -> COPY/mapping -> partitioned raw, without
// contacting GCBA or retaining data in the user's schema.
func TestRealtimeWorkerIntegration(t *testing.T) {
	fixture := newRealtimeFixture(t, 30*time.Second)
	fixture.migrate("migrar_realtime.sql")
	ctx, tx := fixture.ctx, fixture.tx
	repo := fixture.realtimeRepository()
	_, err := tx.Exec(ctx, repo.sql(`
		CREATE TABLE vialis.recorridos (
		    gtfs_route_id text,
		    direction_id smallint,
		    gtfs_shape_id text,
		    geom geometry(LineString, 4326)
		);

		CREATE TABLE vialis.equivalencias_recorridos_gcba (
		    gcba_route_id text,
		    gcba_direction smallint,
		    gtfs_route_id text,
		    gtfs_direction_id smallint
		);

		INSERT INTO vialis.recorridos
		VALUES (
		    'gtfs',
		    0,
		    'shape',
		    ST_GeomFromText('LINESTRING(-58.38 -34.60,-58.39 -34.61)', 4326)
		);

		INSERT INTO vialis.equivalencias_recorridos_gcba
		VALUES (
		    'route-1',
		    0,
		    'gtfs',
		    0
		),
		(
		    'route-1',
		    1,
		    'gtfs',
		    0
		)
	`))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile("../../transport/gcba/testdata/vehicle-positions-normal.json")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	now := time.Date(2025, 1, 1, 0, 0, 30, 0, time.UTC)
	client, err := gcba.NewClient(gcba.Options{ClientID: "private-id", ClientSecret: "private-secret", BaseURL: server.URL, RequestTimeout: time.Second, MaximumAge: 5 * time.Minute, MaximumFutureSkew: time.Minute, MaximumBodyBytes: 10000, MaximumAttempts: 1, InitialBackoff: time.Millisecond, MaximumBackoff: time.Millisecond, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stop := context.WithCancel(ctx)
	defer stop()

	stopping := &stoppingRealtimeRepository{RealtimeRepository: repo, cancel: stop}
	var logs bytes.Buffer
	worker, err := realtime.NewWorker(client, stopping, slog.New(slog.NewJSONHandler(&logs, nil)), realtime.Options{Interval: time.Millisecond, DatabaseTimeout: time.Second, CoverageWarningRatio: .7, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Run(workerCtx); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 || stopping.stores != 2 {
		t.Fatalf("requests=%d stores=%d", requests.Load(), stopping.stores)
	}
	var raw, captures int
	if err = tx.QueryRow(ctx, repo.sql(`
		SELECT count(*)
		FROM vialis.posiciones_colectivos_raw
	`)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, repo.sql(`
		SELECT count(*)
		FROM vialis.capturas_colectivos
	`)).Scan(&captures); err != nil {
		t.Fatal(err)
	}
	if raw != 2 || captures != 2 {
		t.Fatalf("raw=%d captures=%d", raw, captures)
	}
	if strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "vehicle-001") {
		t.Fatal("private data logged")
	}
}
