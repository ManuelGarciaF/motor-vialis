package postgres

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/jackc/pgx/v5"
)

func TestBunchingRecurrenceIntegration(t *testing.T) {
	fixture := newRealtimeFixture(t, time.Minute)
	fixture.migrate("migrar_realtime.sql", "migrar_aglutinaciones.sql", "migrar_aglutinaciones.sql")
	ctx, tx, schema := fixture.ctx, fixture.tx, fixture.schema
	writer := fixture.realtimeRepository()
	if _, err := tx.Exec(ctx, writer.sql(`
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
		    'route',
		    0,
		    'new-shape',
		    ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.5 -34.6)', 4326),
		    'TEST',
		    'A'
		),
		(
		    2,
		    'route',
		    1,
		    'new-shape',
		    ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.5 -34.6)', 4326),
		    'TEST',
		    'A'
		),
		(
		    3,
		    'unmapped',
		    0,
		    'shape',
		    ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.5 -34.6)', 4326),
		    'OTHER',
		    'B'
		);

		INSERT INTO vialis.equivalencias_recorridos_gcba
		VALUES (
		    'gcba',
		    0,
		    'route',
		    0
		),
		(
		    'gcba',
		    1,
		    'route',
		    1
		);
	`)); err != nil {
		t.Fatal(err)
	}
	// The worker's extracted schema check must accept a complete operational
	// schema and reject missing prerequisites. All alterations are rolled back.
	var ready bool
	if err := tx.QueryRow(ctx, checkRealtimeSchemaSQL, schema).Scan(&ready); err != nil || !ready {
		t.Fatalf("complete realtime schema: %v %v", ready, err)
	}
	if err := tx.QueryRow(ctx, checkRealtimeSchemaSQL, schema+"_missing").Scan(&ready); err != nil || ready {
		t.Fatalf("missing realtime schema: %v %v", ready, err)
	}
	for _, column := range []string{"velocidad_maxima_a_metros_segundo", "velocidad_maxima_b_metros_segundo"} {
		nested, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := nested.Exec(ctx, `
			ALTER TABLE
		`+pgx.Identifier{schema, "aglutinaciones_colectivos"}.Sanitize()+" DROP COLUMN "+pgx.Identifier{column}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		if err := nested.QueryRow(ctx, checkRealtimeSchemaSQL, schema).Scan(&ready); err != nil || ready {
			t.Fatalf("missing %s: %v %v", column, ready, err)
		}
		if err := nested.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	parse := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	policy, recurrence := config.BunchingPolicy(), config.BunchingRecurrencePolicy()
	batch := &pgx.Batch{}
	add := func(name, route string, direction int16, start time.Time, duration time.Duration, observations int, closed bool) {
		last := start.Add(duration)
		e := bunching.Event{Scope: bunching.Scope{GCBARouteID: name, GCBADirection: direction, GTFSRouteID: route, GTFSDirection: direction, ShapeID: "old-shape", ShapeHash: "old-hash"}, Line: "HISTORIC", Branch: "A", VehicleA: name + "a", VehicleB: name + "b", FirstObservedAt: start, LastObservedAt: last, ConfirmedAt: last, ObservedA: last, ObservedB: last, MinimumDistanceMeters: 50, LastDistanceMeters: 50, PointA: bunching.Point{Longitude: -58.4, Latitude: -34.6}, PointB: bunching.Point{Longitude: -58.4005, Latitude: -34.6}, Observations: observations, MaximumSpeedAMetersPerSecond: 1, MaximumSpeedBMetersPerSecond: 1}
		if name == "stationary" {
			e.MaximumSpeedAMetersPerSecond, e.MaximumSpeedBMetersPerSecond = 0, 0
		}
		if name == "only-one-moving" {
			e.MaximumSpeedBMetersPerSecond = 0
		}
		if name == "below-speed" {
			e.MaximumSpeedBMetersPerSecond = 1 - 0.000001
		}
		if closed {
			// A delayed closure cannot create evidence in later hours.
			e.ClosedAt = &now
			e.CloseReason = "missing"
		}
		queueEvent(batch, writer, e)
	}
	minimum := policy.MinimumDuration
	add("boundary", "route", 0, now.Add(-recurrence.Window), minimum, 2, true)
	add("clipped-start", "route", 0, now.Add(-recurrence.Window-10*time.Minute), 20*time.Minute, 2, true)
	add("cross-hour", "route", 0, parse("2026-10-08T11:50:00Z"), 30*time.Minute, 2, true) // 08:50–09:20
	add("ends-on-hour", "route", 0, parse("2026-10-09T11:45:00Z"), minimum, 2, false)     // 08:45–09:00
	add("same-day", "route", 0, parse("2026-10-09T11:10:00Z"), minimum, 2, true)
	add("next-slot", "route", 0, parse("2026-10-09T12:30:00Z"), minimum, 2, true)
	add("midnight", "route", 0, parse("2026-10-09T02:50:00Z"), 20*time.Minute, 2, true)
	add("clipped-end", "route", 0, now.Add(-10*time.Minute), 20*time.Minute, 2, false)
	add("stationary", "route", 0, parse("2026-10-05T12:00:00Z"), minimum, 20, true)
	add("only-one-moving", "route", 0, parse("2026-10-06T12:00:00Z"), minimum, 20, true)
	add("below-speed", "route", 0, parse("2026-10-07T12:00:00Z"), minimum, 20, true)
	add("short", "route", 0, parse("2026-10-05T12:00:00Z"), minimum-time.Microsecond, 20, true)
	add("single", "route", 0, parse("2026-10-06T12:00:00Z"), time.Hour, 1, true)
	add("ends-at-start", "route", 0, now.Add(-recurrence.Window-minimum), minimum, 2, true)
	add("at-end", "route", 0, now, minimum, 2, false)
	// Encounters on distinct dates but in different hours are NOT recurrent.
	add("direction-one", "route", 1, parse("2026-10-05T13:20:00Z"), minimum, 2, true)
	add("same-local-day", "route", 1, parse("2026-10-05T13:40:00Z"), minimum, 2, true)
	add("different-hour", "route", 1, parse("2026-10-06T14:20:00Z"), minimum, 2, true)
	// The same encounter can occupy an hour on more than one date, but is
	// still counted only once in that hour and in the overall event total.
	add("multi-day", "multiday", 0, parse("2026-10-06T11:30:00Z"), 26*time.Hour, 2, true)
	add("multi-day-tie", "multiday", 1, parse("2026-10-06T11:30:00Z"), 26*time.Hour, 2, true)
	add("removed-one", "removed", 0, parse("2026-10-05T12:00:00Z"), minimum, 2, true)
	add("removed-two", "removed", 0, parse("2026-10-06T12:00:00Z"), minimum, 2, true)
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	// Session timezone must not change the configured local-hour bucketing.
	if _, err := tx.Exec(ctx, "SET LOCAL TIME ZONE 'Pacific/Auckland'"); err != nil {
		t.Fatal(err)
	}
	reader := fixture.bunchingRepository()
	page, err := reader.FindRecurrence(ctx, nil, now, policy, recurrence)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Lines) != 5 || !page.From.Equal(now.Add(-7*24*time.Hour)) || !page.To.Equal(now) {
		t.Fatalf("page: %+v", page)
	}
	wantOrder := []string{"route/0", "route/1", "removed/0", "multiday/0", "multiday/1"}
	for i, line := range page.Lines {
		if line.EventCount == 0 || fmt.Sprintf("%s/%d", line.GTFSRouteID, line.Direction) != wantOrder[i] {
			t.Fatalf("global filtering/order: %+v", page.Lines)
		}
	}

	var found *bunching.LineRecurrence
	for i := range page.Lines {
		line := &page.Lines[i]
		switch {
		case line.GTFSRouteID == "removed":
			if line.LineID != nil || !line.Recurrent || line.EventCount != 2 || line.Mapped {
				t.Fatalf("removed: %+v", line)
			}
		case line.GTFSRouteID == "multiday":
			if !line.Recurrent || line.EventCount != 1 || line.DaysWithEvents != 2 {
				t.Fatalf("multiday: %+v", line)
			}
			for _, hour := range line.Hours {
				if hour.EventCount != 1 {
					t.Fatalf("duplicated event: %+v", hour)
				}
			}
		case line.Direction == 1:
			if line.Recurrent || line.EventCount != 3 || line.DaysWithEvents != 2 {
				t.Fatalf("direction: %+v", line)
			}
		default:
			found = line
			if !line.Recurrent ||
				line.EventCount != 8 ||
				!line.Mapped ||
				!reflect.DeepEqual(line.Dates, []string{"2026-10-03", "2026-10-08", "2026-10-09", "2026-10-10"}) {
				t.Fatalf("recurrence: %+v", line)
			}
			wantHours := []bunching.HourRecurrence{
				{Hour: 0, EventCount: 1, DaysWithEvents: 1, Dates: []string{"2026-10-09"}},
				{Hour: 8, EventCount: 4, DaysWithEvents: 3, Dates: []string{"2026-10-08", "2026-10-09", "2026-10-10"}, Recurrent: true},
				{Hour: 9, EventCount: 4, DaysWithEvents: 3, Dates: []string{"2026-10-03", "2026-10-08", "2026-10-09"}, Recurrent: true},
				{Hour: 23, EventCount: 1, DaysWithEvents: 1, Dates: []string{"2026-10-08"}},
			}
			if !reflect.DeepEqual(line.Hours, wantHours) {
				t.Fatalf("hours: %+v", line.Hours)
			}
		}
	}

	service, err := bunching.NewService(reader, policy, config.BunchingHistoryPolicy(), recurrence)
	if err != nil {
		t.Fatal(err)
	}
	// Service's real clock is deliberately avoided; repository filtering must
	// match the global result for the exact same cutoff.
	id := int64(1)
	filtered, err := reader.FindRecurrence(ctx, &id, now, policy, recurrence)
	if err != nil || len(filtered.Lines) != 1 || found == nil || !reflect.DeepEqual(filtered.Lines[0], *found) {
		t.Fatalf("filtered: %+v %v", filtered, err)
	}
	// A direct line query must still return a zero summary, even though the
	// same line is excluded from the global list.
	emptyID := int64(3)
	empty, err := reader.FindRecurrence(ctx, &emptyID, now, policy, recurrence)
	if err != nil || len(empty.Lines) != 1 {
		t.Fatalf("empty line: %+v %v", empty, err)
	}
	zero := empty.Lines[0]
	if zero.EventCount != 0 || zero.Recurrent || zero.Dates == nil || zero.Hours == nil || len(zero.Hours) != 0 || zero.Mapped {
		t.Fatalf("zero summary: %+v", zero)
	}
	// Four weekly dates are not sufficient when no individual hour has four.
	stricter := recurrence
	stricter.MinimumDays = 4
	strictPage, err := reader.FindRecurrence(ctx, &id, now, policy, stricter)
	if err != nil || len(strictPage.Lines) != 1 || strictPage.Lines[0].Recurrent || strictPage.Lines[0].DaysWithEvents != 4 {
		t.Fatalf("per-hour threshold: %+v %v", strictPage, err)
	}
	noEvents, err := reader.FindRecurrence(ctx, nil, now.Add(30*24*time.Hour), policy, recurrence)
	if err != nil || noEvents.Lines == nil || len(noEvents.Lines) != 0 {
		t.Fatalf("empty global list: %+v %v", noEvents, err)
	}
	// Without captures, current data is stale/partial but the historical summary
	// must still be present when querying the composed service.
	current, err := service.Get(ctx, 1)
	if err != nil || current.Recurrence.Dates == nil || current.Recurrence.Hours == nil || !current.Stale || !current.Partial {
		t.Fatalf("service current: %+v %v", current, err)
	}
}
