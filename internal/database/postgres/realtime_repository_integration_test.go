package postgres

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/jackc/pgx/v5"
)

func TestRealtimeRepositoryIntegration(t *testing.T) {
	fixture := newRealtimeFixture(t, 2*time.Minute)
	fixture.migrate("migrar_realtime.sql", "migrar_realtime.sql")
	ctx, tx, schema := fixture.ctx, fixture.tx, fixture.schema
	repo := fixture.realtimeRepository()
	_, err := tx.Exec(ctx, repo.sql(`CREATE TABLE vialis.recorridos(id_recorrido bigint,gtfs_route_id text,direction_id smallint,gtfs_shape_id text,geom geometry(LineString,4326),UNIQUE(gtfs_route_id,direction_id));
 CREATE TABLE vialis.equivalencias_recorridos_gcba(gcba_route_id text,gcba_direction smallint,gtfs_route_id text,gtfs_direction_id smallint,PRIMARY KEY(gcba_route_id,gcba_direction));
 INSERT INTO vialis.recorridos VALUES(1,'gtfs',1,'shape-original',ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.5 -34.7)',4326));
 INSERT INTO vialis.equivalencias_recorridos_gcba VALUES('gcba',0,'gtfs',1),('orphan',0,'absent',0)`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	maintained, err := repo.MaintainPartitions(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(maintained.Created) != 8 {
		t.Fatalf("created: %+v", maintained)
	}
	maintained, err = repo.MaintainPartitions(ctx, now)
	if err != nil || len(maintained.Created) != 0 {
		t.Fatalf("repeat: %+v %v", maintained, err)
	}
	position := bunching.Position{VehicleID: "bus", ProviderRouteID: "gcba", ProviderDirection: 0, ObservedAt: now, Latitude: -34.6, Longitude: -58.4, SpeedMetersPerSecond: 2}
	unknown := position
	unknown.VehicleID = "unknown"
	unknown.ProviderRouteID = "unmapped"
	orphan := position
	orphan.VehicleID = "orphan"
	orphan.ProviderRouteID = "orphan"
	expired := position
	expired.VehicleID = "expired"
	expired.ObservedAt = now.AddDate(0, 0, -7)
	capture := bunching.Capture{ReceivedAt: now, Positions: []bunching.Position{position, unknown, orphan, expired}, Stats: bunching.CaptureStats{TotalRows: 4, ValidRows: 4}}
	first, err := repo.StoreCapture(ctx, capture)
	if err != nil {
		t.Fatal(err)
	}
	if first.Inserted != 1 || first.Unmapped != 2 || first.OutsideRetention != 1 {
		t.Fatalf("result: %+v", first)
	}
	second, err := repo.StoreCapture(ctx, capture)
	if err != nil || second.Inserted != 0 {
		t.Fatalf("repeat: %+v %v", second, err)
	}
	var direction int
	var shape string
	err = tx.QueryRow(ctx, repo.sql(`SELECT gtfs_direction_id,gtfs_shape_id FROM vialis.posiciones_colectivos_raw`)).Scan(&direction, &shape)
	if err != nil || direction != 1 || shape != "shape-original" {
		t.Fatalf("mapping: %d %s %v", direction, shape, err)
	}
	// Simulates replacement of the GTFS catalog, including regenerated IDs.
	if _, err = tx.Exec(ctx, repo.sql(`TRUNCATE vialis.recorridos; INSERT INTO vialis.recorridos VALUES(999,'gtfs',1,'shape-new',ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.6 -34.7)',4326))`)); err != nil {
		t.Fatal(err)
	}
	position.ObservedAt = now.Add(time.Second)
	capture.Positions = []bunching.Position{position}
	if _, err = repo.StoreCapture(ctx, capture); err != nil {
		t.Fatal(err)
	}
	var versions int
	if err = tx.QueryRow(ctx, repo.sql(`SELECT count(DISTINCT gtfs_shape_hash) FROM vialis.posiciones_colectivos_raw`)).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("versions: %d %v", versions, err)
	}
	// Invalid geometry/speed must roll back metadata as well as the batch.
	bad := position
	bad.VehicleID = "invalid"
	bad.Latitude = 91
	capture.Positions = []bunching.Position{bad}
	if _, err := repo.StoreCapture(ctx, capture); err == nil {
		t.Fatal("invalid coordinates accepted")
	}
	var count int
	if err := tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.capturas_colectivos`)).Scan(&count); err != nil || count != 3 {
		t.Fatalf("rollback: %d %v", count, err)
	}
	bad.Latitude = -34.6
	bad.ObservedAt = now.Add(61 * time.Second)
	capture.Positions = []bunching.Position{bad}
	if _, err := repo.StoreCapture(ctx, capture); err == nil {
		t.Fatal("future timestamp accepted")
	}
	if os.Getenv("REALTIME_VOLUME_TEST") == "1" {
		const volumeRows = 1000000
		capture.Positions = make([]bunching.Position, volumeRows)
		for i := range capture.Positions {
			p := position
			p.VehicleID = fmt.Sprintf("volume-%d", i%10000)
			p.ObservedAt = now.Add(-time.Duration(i/10000) * 30 * time.Second)
			p.Longitude = -58.5 + float64(i%100)/1000
			p.Latitude = -34.7 + float64((i/100)%100)/1000
			capture.Positions[i] = p
		}
		capture.Stats = bunching.CaptureStats{TotalRows: volumeRows, ValidRows: volumeRows}
		started := time.Now()
		result, err := repo.StoreCapture(ctx, capture)
		if err != nil || result.Inserted != volumeRows {
			t.Fatalf("volume: %+v %v", result, err)
		}
		var total, heap int64
		err = tx.QueryRow(ctx, `SELECT pg_total_relation_size($1::regclass),pg_relation_size($1::regclass)`, pgx.Identifier{schema, "posiciones_colectivos_raw_20260930"}.Sanitize()).Scan(&total, &heap)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%d rows: COPY+insert=%s heap=%d total=%d bytes/row=%.1f", volumeRows, time.Since(started), heap, total, float64(total)/volumeRows)
		if _, err = tx.Exec(ctx, repo.sql(`ANALYZE vialis.posiciones_colectivos_raw`)); err != nil {
			t.Fatal(err)
		}
		rows, err := tx.Query(ctx, repo.sql(`EXPLAIN (ANALYZE, BUFFERS) SELECT id_vehiculo FROM vialis.posiciones_colectivos_raw WHERE dia_observado='2026-09-30' AND gtfs_route_id='gtfs' AND gtfs_direction_id=1 AND observado_en >= '2026-09-30 11:55:00+00' AND ST_DWithin(posicion::geography,ST_SetSRID(ST_MakePoint(-58.45,-34.65),4326)::geography,200)`))
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err = rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			t.Log(line)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	maintained, err = repo.MaintainPartitions(ctx, now.AddDate(0, 0, 7))
	if err != nil {
		t.Fatal(err)
	}
	if len(maintained.Dropped) != 7 {
		t.Fatalf("dropped: %+v", maintained)
	}
	var raw, captures int
	tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.posiciones_colectivos_raw`)).Scan(&raw)
	tx.QueryRow(ctx, repo.sql(`SELECT count(*) FROM vialis.capturas_colectivos`)).Scan(&captures)
	if raw != 0 || captures != 0 {
		t.Fatalf("retention: raw=%d captures=%d", raw, captures)
	}
}

func TestRealtimeMigrationMatchesDDL(t *testing.T) {
	ddl, err := os.ReadFile("../../../sql/ddl.sql")
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../../sql/recorridos/migrar_realtime.sql")
	if err != nil {
		t.Fatal(err)
	}
	block := string(ddl)
	start := strings.Index(block, "-- Capturas operacionales;")
	end := strings.Index(block, "-- Versiones de la red vial")
	if start < 0 || end < start {
		t.Fatal("realtime DDL markers missing")
	}
	block = block[start:end]
	block = strings.ReplaceAll(block, "CREATE TABLE vialis.", "CREATE TABLE IF NOT EXISTS vialis.")
	block = strings.ReplaceAll(block, "CREATE INDEX ", "CREATE INDEX IF NOT EXISTS ")
	block = strings.ReplaceAll(block, "CREATE UNIQUE INDEX ", "CREATE UNIQUE INDEX IF NOT EXISTS ")
	if !strings.Contains(string(migration), block) {
		t.Fatal("realtime migration diverged from fresh database DDL")
	}
}

func TestUTCDay(t *testing.T) {
	local := time.Date(2026, 9, 30, 22, 0, 0, 0, time.FixedZone("BA", -3*3600))
	if got := utcDay(local); got != time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("day: %v", got)
	}
}
