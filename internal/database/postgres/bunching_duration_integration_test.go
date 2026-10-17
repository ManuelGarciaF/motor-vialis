package postgres

import (
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/jackc/pgx/v5"
)

func TestBunchingMinimumDurationIntegration(t *testing.T) {
	fixture := newRealtimeFixture(t, time.Minute)
	fixture.migrate("migrar_realtime.sql", "migrar_aglutinaciones.sql")
	ctx, tx := fixture.ctx, fixture.tx
	repo := fixture.realtimeRepository()
	// Read filters must not ingest captures or acquire the worker's global lock.
	_, err := tx.Exec(ctx, repo.sql(`
		CREATE TABLE vialis.recorridos (
		    id_recorrido bigint,
		    gtfs_route_id text,
		    direction_id smallint,
		    gtfs_shape_id text,
		    geom geometry(LineString, 4326),
		    linea text,
		    ramal text
		);

		CREATE TABLE vialis.equivalencias_recorridos_gcba (
		    gcba_route_id text,
		    gcba_direction smallint,
		    gtfs_route_id text,
		    gtfs_direction_id smallint
		);

		INSERT INTO vialis.recorridos
		VALUES (
		    1,
		    'gtfs',
		    0,
		    'shape',
		    ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.5 -34.6)', 4326),
		    'TEST',
		    'A'
		);

		INSERT INTO vialis.equivalencias_recorridos_gcba
		VALUES (
		    'gcba',
		    0,
		    'gtfs',
		    0
		);
	`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	_, err = tx.Exec(ctx, repo.sql(`
		INSERT INTO vialis.capturas_colectivos (
		    recibido_en,
		    filas_totales,
		    filas_validas,
		    filas_invalidas,
		    filas_duplicadas,
		    filas_stale,
		    cobertura_parcial,
		    aglutinaciones_analizadas
		)
		VALUES (
		    $1,
		    0,
		    0,
		    0,
		    0,
		    0,
		    false,
		    true
		)
	`), now)
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err = tx.QueryRow(ctx, repo.sql(`
		SELECT md5(encode(ST_AsEWKB(geom), 'hex'))
		FROM vialis.recorridos
		WHERE id_recorrido = 1
	`)).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	policy := config.BunchingPolicy()
	minimum := policy.MinimumDuration
	cases := []struct {
		name         string
		duration     time.Duration
		observations int
		age          time.Duration
		closed       bool
	}{
		{"under", minimum - time.Microsecond, 16, 0, false},
		{"exact", minimum, 16, 0, false},
		{"over", minimum + time.Microsecond, 16, 0, false},
		{"closed-short", minimum - time.Minute, 15, 5 * time.Minute, true},
		{"closed-exact", minimum, 16, time.Minute, true},
		{"single-long", minimum + 5*time.Minute, 1, 0, false},
		{"stale-long", minimum + 5*time.Minute, 21, 6 * time.Minute, false},
		{"stationary", minimum, 16, 0, false},
		{"only-a-moving", minimum, 16, 0, false},
		{"only-b-moving", minimum, 16, 0, false},
		{"below-speed", minimum, 16, 0, false},
	}
	batch := &pgx.Batch{}
	for _, tc := range cases {
		last := now.Add(-tc.age)
		e := bunching.Event{
			Line: "TEST", Branch: "A",
			Scope:    bunching.Scope{GCBARouteID: "gcba", GTFSRouteID: "gtfs", ShapeID: "shape", ShapeHash: hash},
			VehicleA: tc.name + "-a", VehicleB: tc.name + "-b",
			FirstObservedAt: last.Add(-tc.duration), LastObservedAt: last,
			ConfirmedAt: last, ObservedA: last, ObservedB: last,
			MinimumDistanceMeters: 50, LastDistanceMeters: 50,
			PointA:                       bunching.Point{Longitude: -58.4, Latitude: -34.6},
			PointB:                       bunching.Point{Longitude: -58.4005, Latitude: -34.6},
			Observations:                 tc.observations,
			MaximumSpeedAMetersPerSecond: 1, MaximumSpeedBMetersPerSecond: 1,
		}
		switch tc.name {
		case "stationary":
			e.MaximumSpeedAMetersPerSecond, e.MaximumSpeedBMetersPerSecond = 0, 0
		case "only-a-moving":
			e.MaximumSpeedBMetersPerSecond = 0
		case "only-b-moving":
			e.MaximumSpeedAMetersPerSecond = 0
		case "below-speed":
			e.MaximumSpeedAMetersPerSecond = 1 - 0.000001
		}

		if tc.closed {
			e.ClosedAt = &now
			e.CloseReason = "missing"
		}
		queueEvent(batch, repo, e)
	}

	if err = tx.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	reader := fixture.bunchingRepository()
	current, err := reader.FindCurrent(ctx, 1, now, policy)
	if err != nil || len(current.Pairs) != 2 {
		t.Fatalf("current should include only exact/over: %+v %v", current, err)
	}
	for _, pair := range current.Pairs {
		if pair.MaximumSpeedAMetersPerSecond != 1 || pair.MaximumSpeedBMetersPerSecond != 1 {
			t.Fatalf("speed scan: %+v", pair)
		}
		if pair.DurationSeconds < minimum.Seconds() || (pair.VehicleA != "exact-a" && pair.VehicleA != "over-a") {
			t.Fatalf("unexpected current pair: %+v", pair)
		}
	}

	query := bunching.HistoryRequest{From: now.Add(-time.Hour), To: now.Add(time.Second), State: "all", Limit: 50}
	page, err := reader.FindHistory(ctx, query, now, policy)
	if err != nil || page.Total != 4 || len(page.Events) != 4 {
		t.Fatalf("history includes only duration-qualified confirmed encounters: %+v %v", page, err)
	}
	for _, e := range page.Events {
		if e.DurationSeconds < minimum.Seconds() || e.Observations < policy.MinimumObservations {
			t.Fatalf("unqualified history: %+v", e)
		}
		if e.VehicleA == "stale-long-a" && e.Active {
			t.Fatal("duration does not override freshness")
		}
	}

	for state, want := range map[string]int64{"open": 3, "closed": 1} {
		query.State = state
		page, err = reader.FindHistory(ctx, query, now, policy)
		if err != nil || page.Total != want || int64(len(page.Events)) != want {
			t.Fatalf("state %s: %+v %v", state, page, err)
		}
	}

	query.State, query.Limit, query.Offset = "all", 1, 0
	first, err := reader.FindHistory(ctx, query, now, policy)
	if err != nil || first.Total != 4 || len(first.Events) != 1 {
		t.Fatalf("first filtered page: %+v %v", first, err)
	}
	query.Offset = 1
	second, err := reader.FindHistory(ctx, query, now, policy)
	if err != nil || second.Total != 4 || len(second.Events) != 1 || second.Events[0].ID == first.Events[0].ID {
		t.Fatalf("second filtered page: %+v %v", second, err)
	}
	query.Offset = 4
	empty, err := reader.FindHistory(ctx, query, now, policy)
	if err != nil || empty.Total != 4 || len(empty.Events) != 0 {
		t.Fatalf("filtered offset: %+v %v", empty, err)
	}
	// A zero duration explicitly disables this filter but still requires two
	// observations; stored short encounters remain queryable after a policy change.
	policy.MinimumDuration = 0
	query.Limit, query.Offset = 50, 0
	page, err = reader.FindHistory(ctx, query, now, policy)
	if err != nil || page.Total != 6 || len(page.Events) != 6 {
		t.Fatalf("disabled duration filter: %+v %v", page, err)
	}
	current, err = reader.FindCurrent(ctx, 1, now, policy)
	if err != nil || len(current.Pairs) != 3 {
		t.Fatalf("disabled current filter: %+v %v", current, err)
	}
	// Raising the speed threshold excludes even the exact 1 m/s cases.
	policy.MinimumDuration = minimum
	policy.MinimumSpeedMetersPerSecond = 1.000001
	page, err = reader.FindHistory(ctx, query, now, policy)
	if err != nil || page.Total != 0 {
		t.Fatalf("raised speed threshold: %+v %v", page, err)
	}
	// Zero disables movement filtering independently of the duration filter.
	policy.MinimumSpeedMetersPerSecond = 0
	page, err = reader.FindHistory(ctx, query, now, policy)
	if err != nil || page.Total != 8 {
		t.Fatalf("disabled speed history: %+v %v", page, err)
	}
	current, err = reader.FindCurrent(ctx, 1, now, policy)
	if err != nil || len(current.Pairs) != 6 {
		t.Fatalf("disabled speed current: %+v %v", current, err)
	}
}
