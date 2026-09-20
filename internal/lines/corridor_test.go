package lines

import (
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func position(longitude, latitude float64) route.Position {
	return route.Position{Longitude: longitude, Latitude: latitude}
}

func path(positions ...route.Position) *route.LineString {
	return &route.LineString{Positions: positions}
}

func TestDrawnPathConcatenatesTheSegmentsInStopOrder(t *testing.T) {
	drawn := route.Route{Stops: []route.Stop{
		{
			Position:   position(-58.38, -34.60),
			PathToNext: path(position(-58.38, -34.60), position(-58.39, -34.60)),
		},
		{
			Position:   position(-58.39, -34.60),
			PathToNext: path(position(-58.39, -34.60), position(-58.40, -34.61)),
		},
		{Position: position(-58.40, -34.61)},
	}}

	got := DrawnPath(drawn)

	want := []route.Position{
		position(-58.38, -34.60),
		position(-58.39, -34.60),
		position(-58.40, -34.61),
	}
	assertPositions(t, got.Positions, want)
}

// A stop whose geometry is missing is joined to the next one by the straight
// segment between them, so a sketch still produces a corridor.
func TestDrawnPathFallsBackToTheStraightSegment(t *testing.T) {
	drawn := route.Route{Stops: []route.Stop{
		{
			Position:   position(-58.38, -34.60),
			PathToNext: path(position(-58.38, -34.60), position(-58.39, -34.60)),
		},
		{Position: position(-58.39, -34.60)},
		{Position: position(-58.41, -34.62)},
	}}

	got := DrawnPath(drawn)

	want := []route.Position{
		position(-58.38, -34.60),
		position(-58.39, -34.60),
		position(-58.41, -34.62),
	}
	assertPositions(t, got.Positions, want)
}

// A path with a single coordinate describes no segment, so it is treated the
// same as a missing one instead of leaving a gap in the corridor.
func TestDrawnPathFallsBackOnADegeneratePath(t *testing.T) {
	drawn := route.Route{Stops: []route.Stop{
		{
			Position:   position(-58.38, -34.60),
			PathToNext: path(position(-58.38, -34.60)),
		},
		{Position: position(-58.40, -34.61)},
	}}

	got := DrawnPath(drawn)

	assertPositions(t, got.Positions, []route.Position{
		position(-58.38, -34.60),
		position(-58.40, -34.61),
	})
}

// The joint two consecutive segments share is one place, and repeating it
// would hand PostGIS a zero-length piece to measure.
func TestDrawnPathDropsTheRepeatedJoint(t *testing.T) {
	drawn := route.Route{Stops: []route.Stop{
		{
			Position: position(-58.38, -34.60),
			PathToNext: path(
				position(-58.38, -34.60),
				position(-58.385, -34.60),
				position(-58.39, -34.60),
			),
		},
		{
			Position:   position(-58.39, -34.60),
			PathToNext: path(position(-58.39, -34.60), position(-58.40, -34.60)),
		},
		{Position: position(-58.40, -34.60)},
	}}

	got := DrawnPath(drawn)

	assertPositions(t, got.Positions, []route.Position{
		position(-58.38, -34.60),
		position(-58.385, -34.60),
		position(-58.39, -34.60),
		position(-58.40, -34.60),
	})
}

func TestDrawnPathOfAnEmptyRouteIsEmpty(t *testing.T) {
	if got := DrawnPath(route.Route{}); len(got.Positions) != 0 {
		t.Fatalf("positions = %#v, want none", got.Positions)
	}
}

// The ranking has to keep a fragment-of relationship below a genuine twin,
// which is exactly what sorting on the weaker coverage buys.
func TestSortByCorridorAffinityRanksMutualOverlapFirst(t *testing.T) {
	fragment := Similarity{
		Line:               Summary{ID: 1},
		CoverageOfProposed: 1.00,
		CoverageOfStored:   0.07,
	}
	twin := Similarity{
		Line:               Summary{ID: 2},
		CoverageOfProposed: 0.85,
		CoverageOfStored:   0.80,
	}
	similarities := []Similarity{fragment, twin}

	sortByCorridorAffinity(similarities)

	if similarities[0].Line.ID != 2 {
		t.Fatalf(
			"first = %d, want the twin (2): the weaker coverage decides, "+
				"so 1.00/0.07 must not outrank 0.85/0.80",
			similarities[0].Line.ID,
		)
	}
}

func TestSortByCorridorAffinityBreaksTiesOnTheStrongerCoverage(t *testing.T) {
	similarities := []Similarity{
		{Line: Summary{ID: 1}, CoverageOfProposed: 0.50, CoverageOfStored: 0.60},
		{Line: Summary{ID: 2}, CoverageOfProposed: 0.50, CoverageOfStored: 0.90},
	}

	sortByCorridorAffinity(similarities)

	if similarities[0].Line.ID != 2 {
		t.Fatalf("first = %d, want 2", similarities[0].Line.ID)
	}
}

// Two identical pairs must still come back in the same order on every run, or
// the same search would answer differently twice.
func TestSortByCorridorAffinityBreaksRemainingTiesOnTheID(t *testing.T) {
	similarities := []Similarity{
		{Line: Summary{ID: 9}, CoverageOfProposed: 0.40, CoverageOfStored: 0.40},
		{Line: Summary{ID: 3}, CoverageOfProposed: 0.40, CoverageOfStored: 0.40},
		{Line: Summary{ID: 7}, CoverageOfProposed: 0.40, CoverageOfStored: 0.40},
	}

	sortByCorridorAffinity(similarities)

	for index, want := range []int64{3, 7, 9} {
		if similarities[index].Line.ID != want {
			t.Fatalf(
				"ids = %d, %d, %d; want 3, 7, 9",
				similarities[0].Line.ID,
				similarities[1].Line.ID,
				similarities[2].Line.ID,
			)
		}
	}
}

func assertPositions(t *testing.T, got, want []route.Position) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("positions = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("positions[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}
