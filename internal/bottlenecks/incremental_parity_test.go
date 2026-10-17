package bottlenecks

import (
	"reflect"
	"testing"
)

func TestIncrementalBatchParityWithContinuityCuts(t *testing.T) {
	l := testLayout(t, 1500, 500)
	ambiguous := observation(90, 850)
	ambiguous.Match = Ambiguous
	changed := observation(180, 1250)
	changed.Scope.ProviderRouteID = "other"
	cases := [][]Observation{
		{observation(0, 250), observation(60, 750), ambiguous, observation(120, 1250)},
		{observation(0, 250), observation(60, 750), observation(241, 1250)},
		{observation(0, 250), observation(60, 750), observation(90, 700), observation(120, 1250)},
		{observation(0, 250), observation(60, 750), changed},
		{observation(0, 250), observation(60, 750), observation(61, 1250)},
	}
	for caseID, rows := range cases {
		expected := measure(t, l, rows...)
		for mask := 0; mask < 1<<(len(rows)-1); mask++ {
			s := captureState(t, l)
			got := Measurements{Passages: []Passage{}, Incomplete: []IncompletePassage{}, Rejections: map[Rejection]int{}}
			start := 0
			id := int64(11)
			for end := 1; end <= len(rows); end++ {
				if end < len(rows) && mask&(1<<(end-1)) == 0 {
					continue
				}

				var r CaptureMeasurement
				s, r = applyCapture(t, l, s, capture(id, rows[start:end]...))
				got.Passages = append(got.Passages, r.Passages...)
				got.Incomplete = append(got.Incomplete, r.Incomplete...)
				for reason, count := range r.Rejections {
					got.Rejections[reason] += count
				}

				s = roundTripState(t, s)
				start = end
				id++
			}
			// Compare actual continuity cuts, not the final batch's technical censoring.
			incomplete := []IncompletePassage{}
			for _, p := range expected.Incomplete {
				if p.Reason != BatchBoundary {
					incomplete = append(incomplete, p)
				}
			}

			if !reflect.DeepEqual(got.Passages, expected.Passages) ||
				!reflect.DeepEqual(got.Incomplete, incomplete) ||
				!reflect.DeepEqual(got.Rejections, expected.Rejections) {
				t.Fatalf("case %d partition %d: got %+v, expected %+v", caseID, mask, got, expected)
			}
		}
	}
}
