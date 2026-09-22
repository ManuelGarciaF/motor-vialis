package postgres

import (
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
)

func TestMergeInsidePiecesPreservesSeparateRouteIntervals(t *testing.T) {
	pieces := []insidePiece{
		{segmentOrder: 0, start: anchor(0, 0.2), end: anchor(0, 1)},
		{segmentOrder: 1, start: anchor(1, 0), end: anchor(1, 0.4)},
		{segmentOrder: 1, start: anchor(1, 0.7), end: anchor(1, 0.9)},
		{segmentOrder: 3, start: anchor(3, 0.1), end: anchor(3, 0.2)},
	}

	got := mergeInsidePieces(pieces)
	if len(got) != 3 {
		t.Fatalf("intervals = %d, want 3: %#v", len(got), got)
	}
	if got[0].Entry.SegmentOrder != 0 || got[0].Entry.Fraction != 0.2 ||
		got[0].Exit.SegmentOrder != 1 || got[0].Exit.Fraction != 0.4 {
		t.Fatalf("first interval = %#v", got[0])
	}
	for index, interval := range got {
		if interval.Order != index {
			t.Fatalf("interval %d order = %d", index, interval.Order)
		}
	}
}

func anchor(segment int, fraction float64) detour.Anchor {
	return detour.Anchor{SegmentOrder: segment, Fraction: fraction}
}
