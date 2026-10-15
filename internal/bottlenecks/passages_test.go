package bottlenecks

import (
	"math"
	"reflect"
	"testing"
	"time"
)

var epoch = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

func testPolicy() MeasurementPolicy {
	return MeasurementPolicy{MaximumGap: 3 * time.Minute, MaximumDistanceMeters: 100, MaximumSpeedKPH: 100}
}

func observation(seconds int, progress float64) Observation {
	return Observation{VehicleID: "v", Scope: Scope{ProviderRouteID: "g", Version: testVersion()}, ObservedAt: epoch.Add(time.Duration(seconds) * time.Second), Match: Matched, ProgressMeters: progress}
}

func testLayout(t *testing.T, length, nominal float64) Layout {
	t.Helper()
	l, err := NewLayout(testVersion(), length, nominal)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func measure(t *testing.T, l Layout, observations ...Observation) Measurements {
	t.Helper()
	m, err := Measure(l, observations, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPassageWithoutInteriorGPS(t *testing.T) {
	// Sector [500,1000] is entirely inside the supporting GPS interval [250,1250].
	m := measure(t, testLayout(t, 1500, 500), observation(0, 250), observation(120, 1250))
	if len(m.Passages) != 1 {
		t.Fatalf("passages: %+v", m)
	}
	p := m.Passages[0]
	if p.Sector.Index != 1 || p.Duration != time.Minute || math.Abs(p.SpeedKPH-30) > 1e-9 || p.InteriorObservations != 0 || !p.SameGPSInterval {
		t.Fatalf("passage: %+v", p)
	}
	if !p.Entered.At.Equal(epoch.Add(30*time.Second)) || !p.Exited.At.Equal(epoch.Add(90*time.Second)) {
		t.Fatal("wrong interpolation")
	}
	if !p.Entered.BeforeAt.Equal(epoch) || !p.Exited.AfterAt.Equal(epoch.Add(120*time.Second)) || p.MaximumSupportingGap != 120*time.Second {
		t.Fatal("lost supporting evidence")
	}
}

func TestPassageWithInteriorGPSAndStop(t *testing.T) {
	m := measure(t, testLayout(t, 1500, 500), observation(0, 250), observation(60, 750), observation(120, 750), observation(180, 1250))
	if len(m.Passages) != 1 {
		t.Fatalf("passages: %+v", m)
	}
	p := m.Passages[0]
	if p.Duration != 120*time.Second || math.Abs(p.SpeedKPH-15) > 1e-9 || p.InteriorObservations != 2 || p.SameGPSInterval || p.MaximumSupportingGap != time.Minute {
		t.Fatalf("passage: %+v", p)
	}
	if !p.Entered.At.Equal(epoch.Add(30*time.Second)) || !p.Exited.At.Equal(epoch.Add(150*time.Second)) {
		t.Fatal("wrong interpolated crossing")
	}
}

func TestExactBoundariesAndBoundaryWait(t *testing.T) {
	m := measure(t, testLayout(t, 1000, 500), observation(0, 0), observation(60, 500), observation(120, 500), observation(180, 1000))
	if len(m.Passages) != 2 {
		t.Fatalf("passages: %+v", m)
	}
	if m.Passages[0].Duration != time.Minute || m.Passages[1].Duration != 2*time.Minute {
		t.Fatal("boundary wait must belong to downstream sector")
	}
	for _, p := range m.Passages {
		if p.InteriorObservations != 0 || p.SameGPSInterval || !p.Entered.At.Equal(p.Entered.BeforeAt) || !p.Exited.At.Equal(p.Exited.AfterAt) {
			t.Fatalf("exact crossing: %+v", p)
		}
	}
}

func TestMultipleCrossingsAndMergedRemainder(t *testing.T) {
	m := measure(t, testLayout(t, 2100, 500), observation(0, 0), observation(120, 2100))
	if len(m.Passages) != 4 {
		t.Fatalf("got %d passages", len(m.Passages))
	}
	for i, p := range m.Passages {
		if p.Sector.Index != i || math.Abs(p.SpeedKPH-63) > 0.00001 || p.InteriorObservations != 0 {
			t.Fatalf("passage: %+v", p)
		}
	}
	if m.Passages[3].Sector.LengthMeters() != 600 {
		t.Fatal("remainder not merged")
	}
}

func TestIncompletePassagesAreNotExtrapolated(t *testing.T) {
	for _, obs := range [][]Observation{
		nil, {observation(0, 250)}, {observation(0, 250), observation(60, 750)},
		{observation(0, 0), observation(60, 250)}, {observation(0, 1000), observation(60, 1000)},
	} {
		m := measure(t, testLayout(t, 1000, 500), obs...)
		if len(m.Passages) != 0 {
			t.Fatalf("extrapolated: %+v", m.Passages)
		}
	}
}

func TestRejectionsBreakContinuity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Observation)
		reason Rejection
	}{
		{"unmatched", func(o *Observation) { o.Match = Unmatched }, UnresolvedMatch},
		{"ambiguous", func(o *Observation) { o.Match = Ambiguous }, UnresolvedMatch},
		{"different hash", func(o *Observation) { o.Scope.Version.ShapeHash = "other" }, WrongVersion},
		{"different direction", func(o *Observation) { o.Scope.Version.Direction = 1 }, WrongVersion},
		{"far from shape", func(o *Observation) { o.DistanceToShapeMeters = 101 }, OutsideShape},
		{"negative progress", func(o *Observation) { o.ProgressMeters = -1 }, OutsideProgress},
		{"past shape", func(o *Observation) { o.ProgressMeters = 1501 }, OutsideProgress},
		{"different provider branch", func(o *Observation) { o.Scope.ProviderRouteID = "other" }, ScopeChanged},
		{"different provider direction", func(o *Observation) { o.Scope.ProviderDirection = 1 }, ScopeChanged},
		{"backwards", func(o *Observation) { o.ProgressMeters = 550 }, Backwards},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := observation(120, 750)
			tc.change(&bad)
			m := measure(t, testLayout(t, 1500, 500), observation(0, 250), observation(60, 600), bad, observation(180, 1250))
			if len(m.Passages) != 0 || m.Rejections[tc.reason] == 0 {
				t.Fatalf("did not break continuity: %+v", m)
			}
		})
	}
}

func TestGapAndSpeedLimits(t *testing.T) {
	l := testLayout(t, 1500, 500)
	m := measure(t, l, observation(0, 250), observation(181, 1250))
	if len(m.Passages) != 0 || m.Rejections[GPSGap] != 1 {
		t.Fatalf("gap: %+v", m)
	}
	m = measure(t, l, observation(0, 250), observation(30, 1250))
	if len(m.Passages) != 0 || m.Rejections[ImplausibleSpeed] != 1 {
		t.Fatalf("speed: %+v", m)
	}
	a, b := observation(0, 250), observation(180, 1250)
	a.DistanceToShapeMeters, b.DistanceToShapeMeters = 100, 100
	if len(measure(t, l, a, b).Passages) != 1 {
		t.Fatal("inclusive quality limits rejected")
	}
	policy := testPolicy()
	policy.MaximumSpeedKPH = 30
	if m, err := Measure(l, []Observation{observation(0, 250), observation(120, 1250)}, policy); err != nil || len(m.Passages) != 1 {
		t.Fatalf("inclusive speed limit: %+v %v", m, err)
	}
}

func TestRecoveryAndRepeatedTraversal(t *testing.T) {
	m := measure(t, testLayout(t, 1500, 500),
		observation(0, 250), observation(60, 750), observation(120, 1250),
		observation(180, 250), observation(240, 750), observation(300, 1250))
	if len(m.Passages) != 2 || m.Rejections[Backwards] != 1 {
		t.Fatalf("recovery: %+v", m)
	}
	if !m.Passages[1].Entered.At.After(m.Passages[0].Exited.At) {
		t.Fatal("mixed traversals")
	}
}

func TestDeterminismDuplicatesAndVehicleIsolation(t *testing.T) {
	l := testLayout(t, 1500, 500)
	a, b := observation(0, 250), observation(120, 1250)
	c, d := a, b
	c.VehicleID, d.VehicleID = "other", "other"
	input := []Observation{b, c, a, d}
	before := append([]Observation(nil), input...)
	got := measure(t, l, input...)
	if len(got.Passages) != 2 || !reflect.DeepEqual(input, before) {
		t.Fatal("cross-vehicle passage or mutated input")
	}
	want := measure(t, l, d, a, c, b)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("non-deterministic output")
	}
	duplicate := a
	duplicate.ObservedAt = duplicate.ObservedAt.In(time.FixedZone("local", -3*3600))
	dup := measure(t, l, a, duplicate, b)
	if dup.Duplicates != 1 || len(dup.Passages) != 1 {
		t.Fatalf("duplicate: %+v", dup)
	}
	conflict := a
	conflict.ProgressMeters++
	if _, err := Measure(l, []Observation{a, conflict, b}, testPolicy()); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	if len(measure(t, l, a, d).Passages) != 0 {
		t.Fatal("joined different vehicles")
	}
}

func TestMeasurementValidation(t *testing.T) {
	l := testLayout(t, 1000, 500)
	if _, err := Measure(Layout{}, nil, testPolicy()); err == nil {
		t.Fatal("zero layout accepted")
	}
	for _, p := range []MeasurementPolicy{{}, {MaximumGap: time.Minute, MaximumDistanceMeters: -1, MaximumSpeedKPH: 100}, {MaximumGap: time.Minute, MaximumDistanceMeters: 100, MaximumSpeedKPH: math.NaN()}} {
		if _, err := Measure(l, nil, p); err == nil {
			t.Fatalf("policy accepted: %+v", p)
		}
	}
	for _, change := range []func(*Observation){
		func(o *Observation) { o.VehicleID = " " }, func(o *Observation) { o.ObservedAt = time.Time{} },
		func(o *Observation) { o.ProgressMeters = math.NaN() }, func(o *Observation) { o.DistanceToShapeMeters = math.Inf(1) },
		func(o *Observation) { o.DistanceToShapeMeters = -1 }, func(o *Observation) { o.Match = "unknown" },
		func(o *Observation) { o.Scope.ProviderDirection = 2 }, func(o *Observation) { o.Scope.Version.ShapeHash = "" },
	} {
		o := observation(0, 0)
		change(&o)
		if _, err := Measure(l, []Observation{o}, testPolicy()); err == nil {
			t.Fatalf("observation accepted: %+v", o)
		}
	}
}
