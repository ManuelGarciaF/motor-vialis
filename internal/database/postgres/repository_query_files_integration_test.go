package postgres

import (
	"reflect"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
)

func TestFindUnmatchedDetourPointsSQL(t *testing.T) {
	fixture := newRealtimeFixture(t, time.Minute)
	_, err := fixture.tx.Exec(fixture.ctx, `
		CREATE TEMP TABLE detour_requested_points (
			id bigint,
			route_order integer
		) ON COMMIT DROP;
		CREATE TEMP TABLE detour_points (pid bigint) ON COMMIT DROP;
		INSERT INTO detour_requested_points VALUES (1, 30), (2, 20), (3, 10);
		INSERT INTO detour_points VALUES (2);
	`)
	if err != nil {
		t.Fatal(err)
	}

	points := []detour.RoutingPoint{
		{ID: 1, Role: detour.PointOptionalStop},
		{ID: 2, Role: detour.PointAnchor},
		{ID: 3, Role: detour.PointOptionalStop},
	}
	result, err := readUnmatchedPoints(fixture.ctx, fixture.tx, points)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.UnmatchedPointIDs, []int64{3, 1}) {
		t.Fatalf("unmatched points/order = %v, want [3 1]", result.UnmatchedPointIDs)
	}
}

func TestRealtimePartitionQueryFiles(t *testing.T) {
	fixture := newRealtimeFixture(t, time.Minute)
	fixture.migrate("migrar_realtime.sql")
	repository := fixture.realtimeRepository()
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

	name, created, err := repository.ensurePartition(fixture.ctx, fixture.tx, day)
	if err != nil || !created || name != "posiciones_colectivos_raw_20261017" {
		t.Fatalf("create isolated partition: name=%q created=%v err=%v", name, created, err)
	}
	nameAgain, createdAgain, err := repository.ensurePartition(fixture.ctx, fixture.tx, day)
	if err != nil || createdAgain || nameAgain != name {
		t.Fatalf("find existing isolated partition: name=%q created=%v err=%v", nameAgain, createdAgain, err)
	}
}
