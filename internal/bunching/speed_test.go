package bunching

import (
	"math"
	"testing"
	"time"
)

func withSpeeds(s Snapshot, a, b float64) Snapshot {
	oa, ob := s.Positions["a"], s.Positions["b"]
	oa.SpeedMetersPerSecond, ob.SpeedMetersPerSecond = a, b
	s.Positions["a"], s.Positions["b"] = oa, ob
	return s
}

func TestMinimumSpeedPolicy(t *testing.T) {
	for _, speed := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1), 0, 1} {
		p := testPolicy()
		p.MinimumSpeedMetersPerSecond = speed
		invalid := speed < 0 || math.IsNaN(speed) || math.IsInf(speed, 0)
		if err := p.Validate(); (err != nil) != invalid {
			t.Fatalf("speed %v: %v", speed, err)
		}
	}
}

func TestEncounterMaximumSpeeds(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	p := testPolicy()
	p.MinimumSpeedMetersPerSecond = 1
	// The canonical pair is a/b even though testSnapshot constructs Key(b,a).
	initial := withSpeeds(testSnapshot(now, 100), 1, 0)
	events, err := Advance(nil, initial, p)
	if err != nil || len(events) != 1 {
		t.Fatalf("opening: %+v %v", events, err)
	}
	e := events[0]
	e.ID = 1
	if e.MaximumSpeedAMetersPerSecond != 1 || e.MaximumSpeedBMetersPerSecond != 0 {
		t.Fatal(e)
	}
	// Each vehicle can reach its maximum in a different accepted sample.
	events, err = Advance([]Event{e}, withSpeeds(testSnapshot(now.Add(time.Minute), 90), 0, 2), p)
	if err != nil || len(events) != 1 {
		t.Fatalf("update: %+v %v", events, err)
	}
	e = events[0]
	if e.MaximumSpeedAMetersPerSecond != 1 || e.MaximumSpeedBMetersPerSecond != 2 {
		t.Fatal(e)
	}
	events, err = Advance([]Event{e}, withSpeeds(testSnapshot(now.Add(2*time.Minute), 90), 0, 0), p)
	if err != nil || len(events) != 1 || events[0].MaximumSpeedAMetersPerSecond != 1 || events[0].MaximumSpeedBMetersPerSecond != 2 {
		t.Fatalf("maxima decreased: %+v %v", events, err)
	}
}

func TestRejectedSamplesDoNotChangeMaximumSpeeds(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	e := firstEvent(t, withSpeeds(testSnapshot(now, 100), 0.2, 0.3))
	for _, tc := range []string{"partial", "duplicate", "one-timestamp", "separated", "identity-changed", "missing", "expired"} {
		t.Run(tc, func(t *testing.T) {
			s := withSpeeds(testSnapshot(now.Add(time.Minute), 100), 20, 30)
			switch tc {
			case "partial":
				s.Partial = true
			case "duplicate":
				s.HasNewPositions = false
				for id, o := range s.Positions {
					o.ObservedAt = now
					s.Positions[id] = o
				}
			case "one-timestamp":
				o := s.Positions["b"]
				o.ObservedAt = now
				s.Positions["b"] = o
			case "separated":
				s.Distances[e.key()] = 250
			case "identity-changed":
				o := s.Positions["a"]
				o.Scope.ShapeHash = "changed"
				s.Positions["a"] = o
			case "missing":
				delete(s.Positions, "b")
				s.Distances = map[PairKey]float64{}
			case "expired":
				s.ReceivedAt = now.Add(6 * time.Minute)
			}
			events, err := Advance([]Event{e}, s, testPolicy())
			if err != nil {
				t.Fatal(err)
			}
			for _, changed := range events {
				if changed.ID == e.ID && (changed.MaximumSpeedAMetersPerSecond != 0.2 || changed.MaximumSpeedBMetersPerSecond != 0.3) {
					t.Fatalf("rejected sample changed maxima: %+v", changed)
				}
			}
		})
	}
}
