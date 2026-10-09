package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBunchingIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	schema := fmt.Sprintf("bunching_test_%d", time.Now().UnixNano())
	if _, err = tx.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	repo := &RealtimeRepository{database: tx, retentionDays: 7, schema: schema}
	for _, path := range []string{"migrar_realtime.sql", "migrar_aglutinaciones.sql", "migrar_aglutinaciones.sql"} {
		data, err := os.ReadFile("../../../sql/recorridos/" + path)
		if err != nil {
			t.Fatal(err)
		}
		script := strings.ReplaceAll(string(data), "\\set ON_ERROR_STOP on", "")
		script = strings.ReplaceAll(script, "BEGIN;", "")
		script = strings.ReplaceAll(script, "COMMIT;", "")
		if _, err = tx.Exec(ctx, repo.sql(script)); err != nil {
			t.Fatal(err)
		}
	}
	_, err = tx.Exec(ctx, repo.sql(`CREATE TABLE vialis.recorridos(id_recorrido bigint,gtfs_route_id text,direction_id smallint,gtfs_shape_id text,geom geometry(LineString,4326),linea text,ramal text);
 CREATE TABLE vialis.equivalencias_recorridos_gcba(gcba_route_id text,gcba_direction smallint,gtfs_route_id text,gtfs_direction_id smallint);
 INSERT INTO vialis.recorridos VALUES(1,'gtfs',0,'shape',ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.5 -34.6)',4326),'TEST','A');
 INSERT INTO vialis.equivalencias_recorridos_gcba VALUES('gcba',0,'gtfs',0),('another',0,'gtfs',0)`))
	if err != nil {
		t.Fatal(err)
	}
	policy := bunching.Policy{DistanceMeters: 200, MaximumAge: 5 * time.Minute, MaximumFutureSkew: time.Minute, MissingCaptureTolerance: 1, MinimumObservations: 2}
	if err = repo.EnableBunching(policy); err != nil {
		t.Fatal(err)
	}
	reader := &BunchingRepository{database: tx, schema: schema}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	capture := bunching.Capture{ReceivedAt: now, Stats: bunching.CaptureStats{TotalRows: 4, ValidRows: 4}}
	for i, id := range []string{"a", "b", "c", "d"} {
		route := "gcba"
		lon := -58.4 - float64(i)*.00165
		if id == "d" {
			route = "another"
			lon = -58.4
		}
		capture.Positions = append(capture.Positions, bunching.Position{VehicleID: id, ProviderRouteID: route, Latitude: -34.6, Longitude: lon, ObservedAt: now, Fresh: true})
	}
	store := func(c bunching.Capture) bunching.StoreResult {
		t.Helper()
		result, err := repo.StoreCapture(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	current := func(at time.Time) bunching.Current {
		t.Helper()
		result, err := reader.FindCurrent(ctx, 1, at, policy)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if store(capture).Inserted != 4 {
		t.Fatal("initial raw")
	}
	if len(current(now).Pairs) != 0 {
		t.Fatal("one observation exposed as confirmed")
	}
	capture.ReceivedAt = now.Add(time.Minute)
	for i := range capture.Positions {
		capture.Positions[i].ObservedAt = capture.ReceivedAt
	}
	store(capture)
	result := current(capture.ReceivedAt)
	if len(result.Pairs) != 2 || result.Stale || result.Partial || !result.Mapped {
		t.Fatalf("current: %+v", result)
	}
	for _, p := range result.Pairs {
		if p.Observations != 2 || p.DurationSeconds != 60 || p.VehicleB == "d" || p.VehicleA == "d" {
			t.Fatal(p)
		}
	}
	historyQuery := bunching.HistoryRequest{From: now.Add(-time.Second), To: now.Add(2 * time.Minute), State: "open", Limit: 1}
	page, err := reader.FindHistory(ctx, historyQuery, now.Add(time.Minute), policy)
	if err != nil || page.Total != 2 || len(page.Events) != 1 || !page.Events[0].Active || page.Events[0].Line != "TEST" || page.Events[0].LineID == nil {
		t.Fatalf("global open: %+v %v", page, err)
	}
	firstID := page.Events[0].ID
	historyQuery.Offset = 1
	page, err = reader.FindHistory(ctx, historyQuery, now.Add(time.Minute), policy)
	if err != nil || page.Total != 2 || len(page.Events) != 1 || page.Events[0].ID == firstID {
		t.Fatalf("pagination: %+v %v", page, err)
	}
	historyQuery.Offset = 0
	page, err = reader.FindHistory(ctx, historyQuery, now.Add(8*time.Minute), policy)
	if err != nil || !page.Stale || page.Events[0].Active {
		t.Fatalf("stale history: %+v %v", page, err)
	}
	// Full response repeats old observations: all vehicles remain present, even
	// though no raw rows are assigned to this capture's ID.
	capture.ReceivedAt = now.Add(2 * time.Minute)
	if store(capture).Inserted != 0 {
		t.Fatal("duplicate raw inserted")
	}
	for _, p := range current(capture.ReceivedAt).Pairs {
		if p.Observations != 2 || !p.LastObservedAt.Equal(now.Add(time.Minute)) {
			t.Fatal("duplicate prolonged event")
		}
	}
	if !current(now.Add(8*time.Minute)).Stale || len(current(now.Add(8*time.Minute)).Pairs) != 0 {
		t.Fatal("stale appeared current")
	}
	capture.ReceivedAt = now.Add(3 * time.Minute)
	capture.Partial = true
	for i := range capture.Positions {
		capture.Positions[i].ObservedAt = capture.ReceivedAt
		if i > 0 {
			capture.Positions[i].Longitude = -58.5 - float64(i)*.01
		}
	}
	store(capture)
	result = current(capture.ReceivedAt)
	if !result.Partial || len(result.Pairs) != 0 {
		t.Fatal("partial exposed pairs")
	}
	page, err = reader.FindHistory(ctx, historyQuery, capture.ReceivedAt, policy)
	if err != nil || !page.Partial || page.Total != 2 || page.Events[0].Active {
		t.Fatalf("partial history: %+v %v", page, err)
	}
	var opened int
	if err = tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.aglutinaciones_colectivos WHERE cerrado_en IS NULL`)).Scan(&opened); err != nil || opened != 2 {
		t.Fatalf("partial closed events: %d %v", opened, err)
	}
	capture.ReceivedAt = now.Add(4 * time.Minute)
	capture.Partial = false
	for i := range capture.Positions {
		capture.Positions[i].ObservedAt = capture.ReceivedAt
	}
	store(capture)
	var separated int
	if err = tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.aglutinaciones_colectivos WHERE motivo_cierre='separated'`)).Scan(&separated); err != nil || separated != 2 {
		t.Fatalf("separation: %d %v", separated, err)
	}
	if len(current(capture.ReceivedAt).Pairs) != 0 {
		t.Fatal("closed events visible")
	}
	if _, err = reader.FindCurrent(ctx, 999, now, policy); !errors.Is(err, lines.ErrNotFound) {
		t.Fatalf("missing line: %v", err)
	}
	historyQuery = bunching.HistoryRequest{From: now.Add(2 * time.Minute), To: now.Add(5 * time.Minute), State: "closed", Limit: 50}
	page, err = reader.FindHistory(ctx, historyQuery, now.Add(4*time.Minute), policy)
	if err != nil || page.Total != 2 || len(page.Events) != 2 {
		t.Fatalf("global closed: %+v %v", page, err)
	}
	for _, e := range page.Events {
		if e.State != "closed" || e.CloseReason != "separated" || e.ClosedAt == nil || e.Active || e.DurationSeconds != 60 {
			t.Fatal(e)
		}
	}
	// Shape ID reuse must not join event history across different geometry.
	capture.ReceivedAt = now.Add(5 * time.Minute)
	for i := range capture.Positions {
		capture.Positions[i].ObservedAt = capture.ReceivedAt
		capture.Positions[i].Longitude = -58.4 - float64(i)*.00165
	}
	store(capture)
	if _, err = tx.Exec(ctx, repo.sql(`UPDATE vialis.recorridos SET linea='RENAMED',geom=ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.6 -34.6)',4326)`)); err != nil {
		t.Fatal(err)
	}
	capture.ReceivedAt = now.Add(6 * time.Minute)
	for i := range capture.Positions {
		capture.Positions[i].ObservedAt = capture.ReceivedAt
	}
	store(capture)
	if len(current(capture.ReceivedAt).Pairs) != 0 {
		t.Fatal("new shape confirmed with old observations")
	}
	var changed int
	if err = tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.aglutinaciones_colectivos WHERE motivo_cierre='identity_changed'`)).Scan(&changed); err != nil || changed != 2 {
		t.Fatalf("changed shape: %d %v", changed, err)
	}
	historyQuery.State = "all"
	page, err = reader.FindHistory(ctx, historyQuery, now.Add(6*time.Minute), policy)
	if err != nil || page.Total != 2 {
		t.Fatalf("archived history: %+v %v", page, err)
	}
	for _, e := range page.Events {
		if e.LineID != nil || e.Line != "TEST" || e.Active {
			t.Fatal("historical identity replaced by new catalog")
		}
	}
	// Actual geodesic boundary test, including ST_DWithin precision at 200m.
	boundary := bunching.Capture{ReceivedAt: now.Add(7 * time.Minute), Stats: bunching.CaptureStats{TotalRows: 6, ValidRows: 6}}
	for i, distance := range []float64{199.99, 200, 200.01} {
		route := fmt.Sprintf("boundary-%d", i)
		if _, err = tx.Exec(ctx, repo.sql(`INSERT INTO vialis.equivalencias_recorridos_gcba VALUES($1,0,'gtfs',0)`), route); err != nil {
			t.Fatal(err)
		}
		var lon, lat float64
		if err = tx.QueryRow(ctx, `SELECT ST_X(p::geometry),ST_Y(p::geometry) FROM (SELECT ST_Project(ST_SetSRID(ST_MakePoint(-58.4,-34.6),4326)::geography,$1::double precision,0::double precision) AS p) q`, distance).Scan(&lon, &lat); err != nil {
			t.Fatal(err)
		}
		boundary.Positions = append(boundary.Positions, bunching.Position{VehicleID: route + "-a", ProviderRouteID: route, Latitude: -34.6, Longitude: -58.4, ObservedAt: boundary.ReceivedAt}, bunching.Position{VehicleID: route + "-b", ProviderRouteID: route, Latitude: lat, Longitude: lon, ObservedAt: boundary.ReceivedAt})
	}
	store(boundary)
	var count int
	if err = tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.aglutinaciones_colectivos WHERE cerrado_en IS NULL AND gcba_route_id IN ('boundary-0','boundary-1')`)).Scan(&count); err != nil || count != 2 {
		t.Fatalf("inclusive radius: %d %v", count, err)
	}
	if err = tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.aglutinaciones_colectivos WHERE gcba_route_id='boundary-2'`)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("outside radius: %d %v", count, err)
	}
	// New full snapshots may change only UNMAPPED rows. Raw insert count is
	// zero, but two distinct snapshots without one vehicle must count as missing.
	var positions []bunching.Position
	for _, p := range boundary.Positions {
		if p.VehicleID != "boundary-1-b" {
			positions = append(positions, p)
		}
	}
	positions = append(positions, bunching.Position{VehicleID: "unmapped", ProviderRouteID: "unknown", Latitude: -34.6, Longitude: -58.4, ObservedAt: now.Add(8 * time.Minute)})
	boundary.Positions = positions
	boundary.ReceivedAt = now.Add(8 * time.Minute)
	if store(boundary).Inserted != 0 {
		t.Fatal("expected all mapped observations to be duplicates")
	}
	var missing int
	if err = tx.QueryRow(ctx, repo.sql(`SELECT capturas_faltantes FROM vialis.aglutinaciones_colectivos WHERE gcba_route_id='boundary-1'`)).Scan(&missing); err != nil || missing != 1 {
		t.Fatalf("unmapped novelty ignored: %d %v", missing, err)
	}
	boundary.ReceivedAt = now.Add(9 * time.Minute)
	store(boundary)
	if err = tx.QueryRow(ctx, repo.sql(`SELECT capturas_faltantes FROM vialis.aglutinaciones_colectivos WHERE gcba_route_id='boundary-1'`)).Scan(&missing); err != nil || missing != 1 {
		t.Fatalf("cached response counted: %d %v", missing, err)
	}
	boundary.ReceivedAt = now.Add(10 * time.Minute)
	boundary.Positions[len(boundary.Positions)-1].ObservedAt = boundary.ReceivedAt
	store(boundary)
	var reason string
	if err = tx.QueryRow(ctx, repo.sql(`SELECT motivo_cierre FROM vialis.aglutinaciones_colectivos WHERE gcba_route_id='boundary-1'`)).Scan(&reason); err != nil || reason != "missing" {
		t.Fatalf("missing closure: %s %v", reason, err)
	}
	if _, err = tx.Exec(ctx, repo.sql(`INSERT INTO vialis.equivalencias_recorridos_gcba VALUES('stable',0,'gtfs',0)`)); err != nil {
		t.Fatal(err)
	}
	stable := bunching.Capture{ReceivedAt: now.Add(11 * time.Minute), Positions: []bunching.Position{
		{VehicleID: "stable-a", ProviderRouteID: "stable", Latitude: -34.6, Longitude: -58.4, ObservedAt: now.Add(11 * time.Minute)},
		{VehicleID: "stable-b", ProviderRouteID: "stable", Latitude: -34.6, Longitude: -58.5, ObservedAt: now.Add(11 * time.Minute)},
	}, Stats: bunching.CaptureStats{TotalRows: 2, ValidRows: 2}}
	store(stable)
	stable.ReceivedAt = now.Add(12 * time.Minute)
	stable.Positions[1].Longitude = -58.4
	stable.Positions = append(stable.Positions, bunching.Position{VehicleID: "unmapped", ProviderRouteID: "unknown", Latitude: -34.6, Longitude: -58.4, ObservedAt: stable.ReceivedAt})
	stable.Stats.TotalRows = 3
	stable.Stats.ValidRows = 3
	store(stable)
	if err = tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.aglutinaciones_colectivos WHERE gcba_route_id='stable'`)).Scan(&count); err != nil || count != 0 {
		t.Fatalf("changed coordinates with duplicate timestamp created encounter: %d %v", count, err)
	}
}
