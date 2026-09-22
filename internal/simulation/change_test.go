package simulation

import (
	"encoding/json"
	"math"
	"testing"
)

func TestNewChangeCalculatesTheRelativeFraction(t *testing.T) {
	change := newChange(200, 250)

	if change.Absolute != 50 {
		t.Fatalf("absolute = %v, want 50", change.Absolute)
	}
	if change.Relative == nil {
		t.Fatal("relative = nil, want 0.25")
	}
	if math.Abs(*change.Relative-0.25) > 1e-9 {
		t.Fatalf("relative = %v, want 0.25", *change.Relative)
	}
}

func TestNewChangeKeepsTheSignOfALoss(t *testing.T) {
	change := newChange(200, 150)

	if change.Absolute != -50 {
		t.Fatalf("absolute = %v, want -50", change.Absolute)
	}
	if change.Relative == nil || math.Abs(*change.Relative+0.25) > 1e-9 {
		t.Fatalf("relative = %v, want -0.25", change.Relative)
	}
}

func TestNewChangeLeavesTheRelativeUndefinedWithoutABaseline(t *testing.T) {
	for _, proposed := range []float64{0, 120} {
		change := newChange(0, proposed)

		if change.Relative != nil {
			t.Fatalf(
				"relative = %v for baseline 0 and proposed %v, want nil",
				*change.Relative,
				proposed,
			)
		}
		if change.Absolute != proposed {
			t.Fatalf("absolute = %v, want %v", change.Absolute, proposed)
		}
	}
}

// Non-finite ratios must not reach JSON encoding.
func TestChangeAlwaysEncodesToJSON(t *testing.T) {
	changes := []any{
		newChange(0, 100),
		newChange(0, 0),
		newIntChange(0, 5),
		newChange(math.SmallestNonzeroFloat64, math.MaxFloat64),
	}

	for _, change := range changes {
		encoded, err := json.Marshal(change)
		if err != nil {
			t.Fatalf("json.Marshal(%#v) error = %v", change, err)
		}
		if !json.Valid(encoded) {
			t.Fatalf("json.Marshal(%#v) = %s, want valid JSON", change, encoded)
		}
	}
}

func TestNewIntChangeCountsInWholeUnits(t *testing.T) {
	change := newIntChange(40, 30)

	if change.Absolute != -10 {
		t.Fatalf("absolute = %d, want -10", change.Absolute)
	}
	if change.Relative == nil || math.Abs(*change.Relative+0.25) > 1e-9 {
		t.Fatalf("relative = %v, want -0.25", change.Relative)
	}
}
