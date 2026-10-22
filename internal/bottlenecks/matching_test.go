package bottlenecks

import (
	"reflect"
	"testing"
)

func candidates(seconds int, progress ...float64) CandidateObservation {
	o := observation(seconds, 0)
	r := CandidateObservation{VehicleID: o.VehicleID, Scope: o.Scope, ObservedAt: o.ObservedAt}
	for _, p := range progress {
		r.Candidates = append(r.Candidates, Projection{ProgressMeters: p, DistanceMeters: 5})
	}

	return r
}

func TestConservativeMatching(t *testing.T) {
	l := testLayout(t, 3000, 500)
	for _, tc := range []struct {
		name     string
		rows     []CandidateObservation
		statuses []MatchStatus
	}{
		{"forward anchor", []CandidateObservation{candidates(0, 250), candidates(60, 750, 2500)}, []MatchStatus{Matched, Matched}},
		{"backward propagation", []CandidateObservation{candidates(0, 250, 2500), candidates(60, 750, 2750), candidates(120, 1250)}, []MatchStatus{Matched, Matched, Matched}},
		{"do not pick nearest", []CandidateObservation{candidates(0, 250), candidates(60, 750, 1000)}, []MatchStatus{Matched, Ambiguous}},
		{"no anchors", []CandidateObservation{candidates(0, 250, 1000), candidates(60, 750, 1500)}, []MatchStatus{Ambiguous, Ambiguous}},
		{"gap barrier", []CandidateObservation{candidates(0, 250), candidates(181, 750, 2500)}, []MatchStatus{Matched, Ambiguous}},
		{"missing barrier", []CandidateObservation{candidates(0, 250), candidates(60), candidates(120, 750, 2500)}, []MatchStatus{Matched, Unmatched, Ambiguous}},
		{"contradictory anchors", []CandidateObservation{candidates(0, 1000), candidates(60, 250, 1500), candidates(120, 500)}, []MatchStatus{Matched, Unmatched, Matched}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MatchConservatively(l, tc.rows, testPolicy())
			if err != nil {
				t.Fatal(err)
			}
			for i, o := range got.Observations {
				if o.Match != tc.statuses[i] {
					t.Fatalf("%d: %+v", i, got)
				}
			}

			reversed := append([]CandidateObservation(nil), tc.rows...)
			for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
				reversed[i], reversed[j] = reversed[j], reversed[i]
			}

			other, err := MatchConservatively(l, reversed, testPolicy())
			if err != nil || !reflect.DeepEqual(got, other) {
				t.Fatal("order dependent", err)
			}
		})
	}
}

func TestMatcherIdentityValidationAndDeduplication(t *testing.T) {
	l := testLayout(t, 3000, 500)
	a, b := candidates(0, 250), candidates(60, 750, 2500)
	b.Scope.ProviderRouteID = "other"
	got, err := MatchConservatively(l, []CandidateObservation{a, b}, testPolicy())
	if err != nil || got.Ambiguous != 1 {
		t.Fatalf("identity leak: %+v %v", got, err)
	}
	b.Scope = a.Scope
	b.VehicleID = "other"
	got, err = MatchConservatively(l, []CandidateObservation{a, b}, testPolicy())
	if err != nil || got.Ambiguous != 1 {
		t.Fatalf("vehicle leak: %+v %v", got, err)
	}
	duplicate := candidates(0, 250, 250)
	got, err = MatchConservatively(l, []CandidateObservation{a, duplicate}, testPolicy())
	if err != nil || got.Duplicates != 1 || len(got.Observations) != 1 {
		t.Fatalf("duplicate: %+v %v", got, err)
	}
	conflict := candidates(0, 300)
	if _, err := MatchConservatively(l, []CandidateObservation{a, conflict}, testPolicy()); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	a.Scope.Version.ShapeHash = "old"
	got, err = MatchConservatively(l, []CandidateObservation{a}, testPolicy())
	if err != nil || got.Unmatched != 1 {
		t.Fatal("wrong version accepted")
	}
	if _, err := MatchConservatively(Layout{}, nil, testPolicy()); err == nil {
		t.Fatal("zero layout")
	}
	if _, err := MatchConservatively(l, nil, MeasurementPolicy{}); err == nil {
		t.Fatal("zero policy")
	}
}
