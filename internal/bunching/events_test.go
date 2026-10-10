package bunching

import (
	"testing"
	"time"
)

func testPolicy() Policy {
	return Policy{DistanceMeters: 200, MaximumAge: 5 * time.Minute, MaximumFutureSkew: time.Minute, MissingCaptureTolerance: 1, MinimumObservations: 2}
}
func testScope() Scope {
	return Scope{GCBARouteID: "gcba", GTFSRouteID: "gtfs", ShapeID: "shape", ShapeHash: "hash"}
}
func testSnapshot(now time.Time, distance float64) Snapshot {
	scope := testScope()
	return Snapshot{ReceivedAt: now, HasNewPositions: true, Positions: map[string]Observation{"a": {VehicleID: "a", Scope: scope, ObservedAt: now, Point: Point{Longitude: -58.4, Latitude: -34.6}}, "b": {VehicleID: "b", Scope: scope, ObservedAt: now, Point: Point{Longitude: -58.401, Latitude: -34.6}}}, Distances: map[PairKey]float64{Key(scope, "b", "a"): distance}}
}
func firstEvent(t *testing.T, s Snapshot) Event {
	t.Helper()
	events, err := Advance(nil, s, testPolicy())
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	e := events[0]
	e.ID = 1
	return e
}

func TestMinimumDurationPolicy(t *testing.T) {
	for _, minimum := range []time.Duration{-time.Nanosecond, 0, 15 * time.Minute} {
		policy := testPolicy()
		policy.MinimumDuration = minimum
		if err := policy.Validate(); (err != nil) != (minimum < 0) {
			t.Fatalf("minimum duration %s: %v", minimum, err)
		}
	}
}

func TestMinimumDurationDoesNotDelayEventCapture(t *testing.T) {
	policy := testPolicy()
	policy.MinimumDuration = 15 * time.Minute
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	events, err := Advance(nil, testSnapshot(now, 100), policy)
	if err != nil || len(events) != 1 || events[0].Observations != 1 {
		t.Fatalf("candidate was not persisted immediately: %+v %v", events, err)
	}
	events[0].ID = 1
	events, err = Advance(events, testSnapshot(now.Add(time.Minute), 90), policy)
	if err != nil || len(events) != 1 || events[0].Observations != 2 {
		t.Fatalf("short encounter stopped tracking: %+v %v", events, err)
	}
}

func TestDistanceBoundariesAndCanonicalPair(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, radius := range []float64{100, 200} {
		policy := testPolicy()
		policy.DistanceMeters = radius
		for _, d := range []float64{radius - 0.01, radius, radius + 0.01} {
			events, err := Advance(nil, testSnapshot(now, d), policy)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if d > radius {
				want = 0
			}
			if len(events) != want {
				t.Fatalf("radius=%v distance=%v count=%d", radius, d, len(events))
			}
			if want == 1 && (events[0].VehicleA != "a" || events[0].VehicleB != "b" || events[0].Observations != 1) {
				t.Fatal(events)
			}
		}
	}
}

func TestBothTimestampsMustAdvance(t *testing.T) {
	now := time.Now()
	event := firstEvent(t, testSnapshot(now, 100))
	s := testSnapshot(now.Add(time.Minute), 90)
	b := s.Positions["b"]
	b.ObservedAt = now
	s.Positions["b"] = b
	events, err := Advance([]Event{event}, s, testPolicy())
	if err != nil || len(events) != 0 {
		t.Fatalf("one unchanged: %+v %v", events, err)
	}
	s = testSnapshot(now.Add(time.Minute), 90)
	events, err = Advance([]Event{event}, s, testPolicy())
	if err != nil || len(events) != 1 || events[0].Observations != 2 || events[0].MinimumDistanceMeters != 90 {
		t.Fatalf("confirmation: %+v %v", events, err)
	}
	repeated := s
	repeated.ReceivedAt = now.Add(2 * time.Minute)
	repeated.HasNewPositions = false
	events, err = Advance(events, repeated, testPolicy())
	if err != nil || len(events) != 0 {
		t.Fatalf("repeated: %+v %v", events, err)
	}
}

func TestSeparatedRequiresFreshNewObservations(t *testing.T) {
	now := time.Now()
	event := firstEvent(t, testSnapshot(now, 100))
	s := testSnapshot(now.Add(time.Minute), 250)
	b := s.Positions["b"]
	b.ObservedAt = now
	s.Positions["b"] = b
	events, _ := Advance([]Event{event}, s, testPolicy())
	if len(events) != 0 {
		t.Fatal("closed using old observation")
	}
	s = testSnapshot(now.Add(time.Minute), 250)
	events, _ = Advance([]Event{event}, s, testPolicy())
	if len(events) != 1 || events[0].CloseReason != "separated" {
		t.Fatal(events)
	}
}

func TestPartialNeverOpensOrCloses(t *testing.T) {
	now := time.Now()
	event := firstEvent(t, testSnapshot(now, 100))
	s := testSnapshot(now.Add(10*time.Minute), 100)
	s.Partial = true
	changes, err := Advance([]Event{event}, s, testPolicy())
	if err != nil || len(changes) != 0 {
		t.Fatalf("partial: %+v %v", changes, err)
	}
	changes, err = Advance(nil, s, testPolicy())
	if err != nil || len(changes) != 0 {
		t.Fatal("partial opened event")
	}
}

func TestMissingToleranceAndDuplicates(t *testing.T) {
	now := time.Now()
	event := firstEvent(t, testSnapshot(now, 100))
	s := testSnapshot(now.Add(time.Minute), 100)
	delete(s.Positions, "b")
	s.Distances = map[PairKey]float64{}
	changes, _ := Advance([]Event{event}, s, testPolicy())
	if len(changes) != 1 || changes[0].MissingCaptures != 1 || changes[0].ClosedAt != nil {
		t.Fatal(changes)
	}
	event = changes[0]
	s.ReceivedAt = now.Add(2 * time.Minute)
	s.HasNewPositions = false
	changes, _ = Advance([]Event{event}, s, testPolicy())
	if len(changes) != 0 {
		t.Fatal("duplicate incremented missing count")
	}
	s.HasNewPositions = true
	changes, _ = Advance([]Event{event}, s, testPolicy())
	if len(changes) != 1 || changes[0].CloseReason != "missing" {
		t.Fatal(changes)
	}
}

func TestExpiredEventDoesNotBridgeGap(t *testing.T) {
	now := time.Now()
	event := firstEvent(t, testSnapshot(now, 100))
	changes, _ := Advance([]Event{event}, testSnapshot(now.Add(6*time.Minute), 80), testPolicy())
	if len(changes) != 2 || changes[0].CloseReason != "expired" || changes[1].ID != 0 || changes[1].Observations != 1 {
		t.Fatal(changes)
	}
}

func TestStaleCannotOpen(t *testing.T) {
	now := time.Now()
	s := testSnapshot(now, 100)
	for id, p := range s.Positions {
		p.ObservedAt = now.Add(-6 * time.Minute)
		s.Positions[id] = p
	}
	changes, _ := Advance(nil, s, testPolicy())
	if len(changes) != 0 {
		t.Fatal(changes)
	}
}

func TestIdentityChanged(t *testing.T) {
	now := time.Now()
	event := firstEvent(t, testSnapshot(now, 100))
	s := testSnapshot(now.Add(time.Minute), 100)
	a := s.Positions["a"]
	a.Scope.ShapeHash = "new"
	s.Positions["a"] = a
	changes, _ := Advance([]Event{event}, s, testPolicy())
	if len(changes) != 1 || changes[0].CloseReason != "identity_changed" {
		t.Fatal(changes)
	}
}

func TestPairsAreNotTransitive(t *testing.T) {
	now := time.Now()
	s := testSnapshot(now, 150)
	c := s.Positions["b"]
	c.VehicleID = "c"
	s.Positions["c"] = c
	s.Distances[Key(testScope(), "b", "c")] = 150
	changes, _ := Advance(nil, s, testPolicy())
	if len(changes) != 2 {
		t.Fatal(changes)
	}
	for _, e := range changes {
		if e.VehicleA == "a" && e.VehicleB == "c" {
			t.Fatal("transitive pair created")
		}
	}
}

func TestDelayedPositionsDoNotCausePrematureExpiry(t *testing.T) {
	now := time.Now()
	s := testSnapshot(now, 100)
	for id, p := range s.Positions {
		p.ObservedAt = now.Add(-4*time.Minute - 30*time.Second)
		s.Positions[id] = p
	}
	event := firstEvent(t, s)
	s.ReceivedAt = now.Add(time.Minute)
	for id, p := range s.Positions {
		p.ObservedAt = p.ObservedAt.Add(time.Minute)
		s.Positions[id] = p
	}
	changes, err := Advance([]Event{event}, s, testPolicy())
	if err != nil || len(changes) != 1 || changes[0].ClosedAt != nil || changes[0].Observations != 2 {
		t.Fatalf("delayed continuation: %+v %v", changes, err)
	}
}

func TestFutureSkewDoesNotCreateInvalidClosureTime(t *testing.T) {
	now := time.Now()
	s := testSnapshot(now, 100)
	for id, p := range s.Positions {
		p.ObservedAt = now.Add(30 * time.Second)
		s.Positions[id] = p
	}
	event := firstEvent(t, s)
	s = testSnapshot(now.Add(10*time.Second), 250)
	for id, p := range s.Positions {
		p.ObservedAt = now.Add(40 * time.Second)
		s.Positions[id] = p
	}
	changes, err := Advance([]Event{event}, s, testPolicy())
	if err != nil || len(changes) != 1 || changes[0].ClosedAt == nil || changes[0].ClosedAt.Before(event.LastObservedAt) {
		t.Fatalf("future closure: %+v %v", changes, err)
	}
}

func TestCachedSamplesCannotStartAnotherEpisode(t *testing.T) {
	now := time.Now()
	s := testSnapshot(now, 100)
	e := firstEvent(t, s)
	s.Previous = map[PairKey]PreviousSample{Key(e.Scope, e.VehicleA, e.VehicleB): {ObservedA: e.ObservedA, ObservedB: e.ObservedB}}
	changes, err := Advance(nil, s, testPolicy())
	if err != nil || len(changes) != 0 {
		t.Fatalf("cached reopening: %+v %v", changes, err)
	}
	for id, p := range s.Positions {
		p.ObservedAt = now.Add(time.Minute)
		s.Positions[id] = p
	}
	s.ReceivedAt = now.Add(time.Minute)
	changes, err = Advance(nil, s, testPolicy())
	if err != nil || len(changes) != 1 {
		t.Fatalf("new episode: %+v %v", changes, err)
	}
}

func TestPairTimeDoesNotBackdateToOlderCachedVehicle(t *testing.T) {
	now := time.Now()
	s := testSnapshot(now, 100)
	p := s.Positions["a"]
	p.ObservedAt = now.Add(-4 * time.Minute)
	s.Positions["a"] = p
	e := firstEvent(t, s)
	if !e.FirstObservedAt.Equal(now) {
		t.Fatalf("backdated encounter: %s", e.FirstObservedAt)
	}
}

func TestMidpointAcrossDateLine(t *testing.T) {
	p := Midpoint(Point{Longitude: 179.999, Latitude: 0}, Point{Longitude: -179.999, Latitude: 0})
	if p.Longitude < 179.99 && p.Longitude > -179.99 {
		t.Fatal(p)
	}
}
