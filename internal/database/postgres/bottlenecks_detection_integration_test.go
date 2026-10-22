package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
)

// All writes are session TEMP fixtures inside a rolled-back transaction. No
// producer tables, checkpoints, raw partitions or persistent schemas are touched.
func TestBottlenecksDetectionRepository(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `
 CREATE TEMP TABLE bottlenecks_modelos(nombre text,checkpoint bigint,ultima_captura_recibida_en timestamptz);
 INSERT INTO bottlenecks_modelos VALUES ('operational',123,'2026-10-21T23:00:00Z');
 CREATE TEMP TABLE bottlenecks_trazados(id bigint,modelo text,gtfs_route_id text,gtfs_shape_hash text,linea text,ramal text,direction_id int);
 INSERT INTO bottlenecks_trazados VALUES (1,'operational','route-a','hash-a','132','A',0),(2,'operational','route-b','hash-b','60','B',1),(3,'other','other','hash-c','132','A',0);
 CREATE TEMP TABLE bottlenecks_sectores(trazado bigint,indice int,inicio_metros float8,fin_metros float8,geom geometry);
 INSERT INTO bottlenecks_sectores SELECT 1,i,i*500,(i+1)*500,ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.40 -34.6)',4326) FROM generate_series(0,4)i;
 INSERT INTO bottlenecks_sectores SELECT t,0,0,600,ST_GeomFromText('LINESTRING(-58.41 -34.6,-58.40 -34.6)',4326) FROM generate_series(2,3)t;
 CREATE TEMP TABLE bottlenecks_mediciones(modelo text,trazado bigint,sector int,completa boolean,velocidad_kph float8,entrada_en timestamptz,vehiculo text);
 INSERT INTO bottlenecks_mediciones VALUES
 ('operational',1,0,true,3,'2026-10-21T21:00:00Z','same-unit'),
 ('operational',1,0,true,5,'2026-10-21T21:01:00Z','same-unit'),
 ('operational',1,0,true,6,'2026-10-21T21:02:00Z','same-unit'),
 ('operational',1,0,true,10,'2026-10-21T21:03:00Z','same-unit'),
 ('operational',1,0,false,null,'2026-10-21T21:04:00Z','same-unit'),
 ('operational',1,1,true,5,'2026-10-21T21:00:00Z','one'),
 ('operational',1,2,true,6,'2026-10-21T21:00:00Z','one'),
 ('operational',1,2,true,6,'2026-10-21T21:01:00Z','two'),
 ('operational',1,3,false,null,'2026-10-21T21:00:00Z','one'),
 ('operational',1,3,false,null,'2026-10-21T21:01:00Z','two'),
 ('operational',1,4,true,1,'2026-10-21T20:59:59Z','one'),
 ('operational',1,4,true,1,'2026-10-21T23:00:00Z','two'),
 ('operational',2,0,true,5,'2026-10-21T21:00:00Z','one'),
 ('operational',2,0,true,5,'2026-10-21T21:01:00Z','two'),
 ('other',3,0,true,1,'2026-10-21T21:00:00Z','one'),
 ('other',3,0,true,1,'2026-10-21T21:01:00Z','two');
 `)
	if err != nil {
		t.Fatal(err)
	}
	repo := &BottlenecksDetectionRepository{database: tx, schema: "pg_temp"}
	from := time.Date(2026, 10, 21, 21, 0, 0, 0, time.UTC)
	q := bottlenecks.DetectionQuery{From: from, To: from.Add(2 * time.Hour)}
	p := bottlenecks.DetectionPolicy{SpeedBelowKPH: 6, MinimumSlowPassages: 2}
	data, err := repo.FindDetectedSectors(ctx, "operational", q, p)
	if err != nil {
		t.Fatal(err)
	}
	if !data.Source.Initialized || data.Source.Checkpoint != 123 || data.Source.LastCaptureReceivedAt == nil || len(data.Sectors) != 2 {
		t.Fatalf("unexpected result %+v", data)
	}
	sector := data.Sectors[0]
	if sector.LayoutID != 1 || sector.Sector != 0 || sector.SlowPassages != 2 || sector.CompletePassages != 4 || sector.IncompletePassages != 1 || sector.SlowPercentage != 50 || sector.MedianSpeedKPH != 5.5 || !sector.RouteEnd || sector.LengthMeters != 500 || len(sector.Geometry.Positions) != 2 {
		t.Fatalf("bad metrics %+v", sector)
	}
	if data.Sectors[1].LayoutID != 2 || data.Sectors[1].Direction != 1 {
		t.Fatalf("unexpected consolidation %+v", data.Sectors)
	}
	for _, tc := range []struct {
		name, line string
		bounds     *bottlenecks.DetectionBounds
		count      int
	}{
		{name: "exact line", line: "132", count: 1},
		{name: "not substring", line: "13", count: 0},
		{name: "literal injection", line: "' OR true --", count: 0},
		{name: "bbox hit", bounds: &bottlenecks.DetectionBounds{MinLongitude: -58.42, MinLatitude: -34.61, MaxLongitude: -58.39, MaxLatitude: -34.59}, count: 2},
		{name: "bbox miss", bounds: &bottlenecks.DetectionBounds{MinLongitude: 0, MinLatitude: 0, MaxLongitude: 1, MaxLatitude: 1}, count: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filtered := q
			filtered.Line = tc.line
			filtered.Bounds = tc.bounds
			got, err := repo.FindDetectedSectors(ctx, "operational", filtered, p)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Sectors) != tc.count {
				t.Fatalf("got %d sectors", len(got.Sectors))
			}
		})
	}
	absent, err := repo.FindDetectedSectors(ctx, "absent", q, p)
	if err != nil {
		t.Fatal(err)
	}
	if absent.Source.Initialized || absent.Source.Checkpoint != 0 || absent.Source.LastCaptureReceivedAt != nil || absent.Sectors == nil || len(absent.Sectors) != 0 {
		t.Fatalf("absent %+v", absent)
	}
	p.MinimumSlowPassages = 3
	got, err := repo.FindDetectedSectors(ctx, "operational", q, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sectors) != 0 {
		t.Fatal("threshold ignored")
	}
}
