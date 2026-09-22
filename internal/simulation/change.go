package simulation

import "math"

// Change reports absolute and relative movement from a baseline.
// Relative is nil when the baseline is zero.
type Change struct {
	Baseline float64  `json:"baseline"`
	Proposed float64  `json:"proposed"`
	Absolute float64  `json:"absolute"`
	Relative *float64 `json:"relative"`
}

// IntChange is Change for values counted in whole units.
type IntChange struct {
	Baseline int64    `json:"baseline"`
	Proposed int64    `json:"proposed"`
	Absolute int64    `json:"absolute"`
	Relative *float64 `json:"relative"`
}

func newChange(baseline, proposed float64) Change {
	return Change{
		Baseline: baseline,
		Proposed: proposed,
		Absolute: proposed - baseline,
		Relative: relativeChange(baseline, proposed-baseline),
	}
}

func newIntChange(baseline, proposed int64) IntChange {
	return IntChange{
		Baseline: baseline,
		Proposed: proposed,
		Absolute: proposed - baseline,
		Relative: relativeChange(float64(baseline), float64(proposed-baseline)),
	}
}

// relativeChange returns nil for ratios that JSON cannot represent.
func relativeChange(baseline, absolute float64) *float64 {
	if baseline == 0 {
		return nil
	}
	relative := absolute / baseline
	if math.IsNaN(relative) || math.IsInf(relative, 0) {
		return nil
	}
	return &relative
}
