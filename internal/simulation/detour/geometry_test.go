package detour

import (
	"errors"
	"math"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestValidateCutRejectsInvalidGeometryAndLimits(t *testing.T) {
	basePolicy := testPolicy()
	tests := []struct {
		name   string
		cut    Cut
		policy Policy
	}{
		{name: "too few positions", cut: cutAt()},
		{name: "non finite", cut: cutAt(position(0, 0), position(math.NaN(), 1))},
		{name: "out of range", cut: cutAt(position(0, 0), position(91, 1))},
		{name: "zero segment", cut: cutAt(position(0, 0), position(0, 0))},
		{
			name: "too many positions",
			cut:  cutAt(position(0, 0), position(0, 0.001), position(0, 0.002)),
			policy: func() Policy {
				value := basePolicy
				value.MaximumCutPositions = 2
				return value
			}(),
		},
		{
			name: "too long",
			cut:  cutAt(position(0, 0), position(0, 0.01)),
			policy: func() Policy {
				value := basePolicy
				value.MaximumCutLengthMeters = 100
				return value
			}(),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := test.policy
			if policy.MaximumCutPositions == 0 {
				policy = basePolicy
			}
			err := ValidateCut(test.cut, policy)
			var detourError *Error
			if !errors.As(err, &detourError) || detourError.Code != ErrorInvalidCut {
				t.Fatalf("ValidateCut error = %v, want invalid_cut", err)
			}
		})
	}
}

func TestValidateCutAcceptsMultipleBranches(t *testing.T) {
	// A T-shaped cut: a trunk plus a branch leaving its middle.
	cut := cutWith(
		[]route.Position{position(0, 0), position(0, 0.01)},
		[]route.Position{position(0, 0.005), position(0.005, 0.005)},
	)
	if err := ValidateCut(cut, testPolicy()); err != nil {
		t.Fatalf("ValidateCut multi-branch error = %v, want nil", err)
	}
}

func TestValidateCutRejectsEmptyCut(t *testing.T) {
	err := ValidateCut(Cut{Lines: nil}, testPolicy())
	var detourError *Error
	if !errors.As(err, &detourError) || detourError.Code != ErrorInvalidCut {
		t.Fatalf("ValidateCut empty error = %v, want invalid_cut", err)
	}
}

func TestValidateCutSumsLengthAcrossBranches(t *testing.T) {
	// Each branch is ~1.1 km; the policy cap sits between one and two branches,
	// so the pair must be rejected even though each branch fits alone.
	cut := cutWith(
		[]route.Position{position(0, 0), position(0, 0.01)},
		[]route.Position{position(0, 0.02), position(0, 0.03)},
	)
	policy := testPolicy()
	policy.MaximumCutLengthMeters = 1500
	err := ValidateCut(cut, policy)
	var detourError *Error
	if !errors.As(err, &detourError) || detourError.Code != ErrorInvalidCut {
		t.Fatalf("ValidateCut summed length error = %v, want invalid_cut", err)
	}
}

func TestClassifyStopsUsesNearestBranch(t *testing.T) {
	// The stop sits far from the first branch but on top of the second, so the
	// minimum-distance classification must treat it as reached by the cut.
	cut := cutWith(
		[]route.Position{position(0, 0), position(0, 0.01)},
		[]route.Position{position(0.05, 0.05), position(0.05, 0.06)},
	)
	input := route.Route{Stops: []route.Stop{
		{ID: "on-second-branch", Position: position(0.05, 0.055)},
	}}

	got := ClassifyStops(input, cut, testPolicy())
	if got[0].Availability != StopForcedUnavailable {
		t.Fatalf("stop availability = %q, want forced_unavailable (distance %.2f)",
			got[0].Availability, got[0].DistanceMeters)
	}
}

func TestClassifyStopsMeasuresClosestPointOnSegment(t *testing.T) {
	input := route.Route{Stops: []route.Stop{
		{ID: "forced", Position: position(0.00009, 0.005)},
		{ID: "optional", Position: position(0.0009, 0.005)},
		{ID: "required", Position: position(0.0054, 0.005)},
	}}
	cut := cutAt(position(0, 0), position(0, 0.01))

	got := ClassifyStops(input, cut, testPolicy())
	want := []StopAvailability{
		StopForcedUnavailable,
		StopOptional,
		StopRequired,
	}
	for index := range want {
		if got[index].Availability != want[index] {
			t.Errorf("stop %d availability = %q, want %q (distance %.2f)",
				index, got[index].Availability, want[index], got[index].DistanceMeters)
		}
	}
	if got[1].DistanceMeters < 95 || got[1].DistanceMeters > 105 {
		t.Fatalf("distance to segment = %.2f, want approximately 100 m", got[1].DistanceMeters)
	}
}

func TestClassifyStopsIncludesRadiusBoundaries(t *testing.T) {
	cut := cutAt(position(0, 0), position(0, 0.01))
	forcedPosition := position(0.0002, 0.005)
	optionalPosition := position(0.001, 0.005)
	forcedDistance := distanceToLineString(forcedPosition, cut.Lines[0])
	optionalDistance := distanceToLineString(optionalPosition, cut.Lines[0])
	policy := testPolicy()
	policy.ForcedStopRadiusMeters = forcedDistance
	policy.OptionalStopRadiusMeters = optionalDistance
	input := route.Route{Stops: []route.Stop{
		{ID: "forced-boundary", Position: forcedPosition},
		{ID: "optional-boundary", Position: optionalPosition},
	}}

	got := ClassifyStops(input, cut, policy)
	if got[0].Availability != StopForcedUnavailable {
		t.Fatalf("forced boundary classified as %q", got[0].Availability)
	}
	if got[1].Availability != StopOptional {
		t.Fatalf("optional boundary classified as %q", got[1].Availability)
	}
}

func TestDistanceToSegmentUsesEndpointOutsideArc(t *testing.T) {
	start := position(0, 0)
	end := position(0, 0.001)
	point := position(0, 0.002)

	got := distanceToSegment(point, start, end)
	want := route.DistanceMeters(point, end)
	if math.Abs(got-want) > 0.001 {
		t.Fatalf("distanceToSegment = %.6f, want endpoint distance %.6f", got, want)
	}
}

func testPolicy() Policy {
	return Policy{
		ForbiddenCorridorMeters:          5,
		ForcedStopRadiusMeters:           20,
		OptionalStopRadiusMeters:         500,
		SearchRadiusMeters:               1000,
		MaximumCutPositions:              10_000,
		MaximumCutLengthMeters:           20_000,
		MaximumTrafficTiles:              32,
		TrafficZoom:                      14,
		TrafficMatchRadiusMeters:         15,
		TrafficDirectionToleranceDegrees: 45,
		TrafficEstimateRadiusMeters:      300,
		TrafficEstimateMinimumSamples:    3,
		TrafficEstimateMaximumSamples:    5,
		PointDirectionToleranceDegrees:   60,
		DefaultSpeedKPH:                  20,
	}
}

func cutAt(positions ...route.Position) Cut {
	return Cut{Lines: []route.LineString{{Positions: positions}}}
}

func cutWith(lines ...[]route.Position) Cut {
	cut := Cut{Lines: make([]route.LineString, len(lines))}
	for index, positions := range lines {
		cut.Lines[index] = route.LineString{Positions: positions}
	}
	return cut
}

func position(latitude, longitude float64) route.Position {
	return route.Position{Latitude: latitude, Longitude: longitude}
}
