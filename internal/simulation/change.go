package simulation

import "math"

// Change reports one measured value in both versions of a route and how it
// moved between them.
//
// Relative is a fraction of the baseline value, so 0.12 means twelve percent
// more than the baseline. It is null when the baseline is zero, because the
// ratio is undefined there: adding demand to a route that carried none is not
// infinite growth, it is an absolute gain and only Absolute describes it.
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

// relativeChange returns nil for any ratio JSON cannot represent, so that an
// edge case cannot fail encoding after the response status has been written.
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
