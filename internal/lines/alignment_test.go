package lines

import (
	"errors"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// testToleranceMeters mirrors the production default in internal/config; the
// alignment rules are the same whatever value is configured.
const testToleranceMeters = 250.0

func TestAlignStoredPathEndpointsProducesAValidRoute(t *testing.T) {
	input := storedRoute()

	if err := AlignStoredPathEndpoints(&input, testToleranceMeters); err != nil {
		t.Fatalf("AlignStoredPathEndpoints() error = %v", err)
	}
	if err := route.Validate(input); err != nil {
		t.Fatalf("aligned route validation error = %v", err)
	}

	for index := 0; index < len(input.Stops)-1; index++ {
		positions := input.Stops[index].PathToNext.Positions
		if positions[0] != input.Stops[index].Position {
			t.Fatalf(
				"stops[%d] path origin = %#v, want %#v",
				index,
				positions[0],
				input.Stops[index].Position,
			)
		}
		if positions[len(positions)-1] != input.Stops[index+1].Position {
			t.Fatalf(
				"stops[%d] path destination = %#v, want %#v",
				index,
				positions[len(positions)-1],
				input.Stops[index+1].Position,
			)
		}
	}
}

func TestAlignStoredPathEndpointsKeepsIntermediatePositions(t *testing.T) {
	input := storedRoute()
	middle := input.Stops[0].PathToNext.Positions[1]

	if err := AlignStoredPathEndpoints(&input, testToleranceMeters); err != nil {
		t.Fatalf("AlignStoredPathEndpoints() error = %v", err)
	}

	positions := input.Stops[0].PathToNext.Positions
	if len(positions) != 3 {
		t.Fatalf("positions = %d, want 3", len(positions))
	}
	if positions[1] != middle {
		t.Fatalf("intermediate position = %#v, want %#v", positions[1], middle)
	}
}

func TestAlignStoredPathEndpointsRejectsImplausibleGap(t *testing.T) {
	input := route.Route{
		Jurisdiction: route.JurisdictionCABA,
		Stops: []route.Stop{
			{
				ID:       "A",
				Position: route.Position{Latitude: -34.6, Longitude: -58.38},
				PathToNext: &route.LineString{Positions: []route.Position{
					{Latitude: -34.6, Longitude: -58.38},
					{Latitude: -34.605, Longitude: -58.385},
					{Latitude: -34.62, Longitude: -58.40},
				}},
			},
			{ID: "B", Position: route.Position{Latitude: -34.61, Longitude: -58.39}},
		},
	}

	err := AlignStoredPathEndpoints(&input, testToleranceMeters)
	if err == nil {
		t.Fatal("AlignStoredPathEndpoints() error = nil")
	}
	var gapError *GapError
	if !errors.As(err, &gapError) {
		t.Fatalf("error = %v, want *GapError", err)
	}
	if gapError.StopIndex != 0 || gapError.AtStart {
		t.Fatalf("gap error = %#v, want the end of stops[0]", gapError)
	}
	if gapError.GapMeters <= testToleranceMeters {
		t.Fatalf(
			"gap = %v, want more than the %v m tolerance",
			gapError.GapMeters,
			testToleranceMeters,
		)
	}
}

// A reversed path must reach route.Validate untouched: aligning its endpoints
// would silently turn a direction mistake into a plausible-looking route.
func TestAlignStoredPathEndpointsLeavesReversedPathsUntouched(t *testing.T) {
	origin := route.Position{Latitude: -34.6, Longitude: -58.38}
	destination := route.Position{Latitude: -34.61, Longitude: -58.39}
	input := route.Route{
		Jurisdiction: route.JurisdictionCABA,
		Stops: []route.Stop{
			{
				ID:       "A",
				Position: origin,
				PathToNext: &route.LineString{Positions: []route.Position{
					destination,
					origin,
				}},
			},
			{ID: "B", Position: destination},
		},
	}

	if err := AlignStoredPathEndpoints(&input, testToleranceMeters); err != nil {
		t.Fatalf("AlignStoredPathEndpoints() error = %v", err)
	}

	positions := input.Stops[0].PathToNext.Positions
	if positions[0] != destination || positions[1] != origin {
		t.Fatalf("reversed path = %#v, want it unchanged", positions)
	}
	if route.Validate(input) == nil {
		t.Fatal("route.Validate() error = nil, want the reversed path reported")
	}
}

func TestAlignStoredPathEndpointsIgnoresPathsItCannotAlign(t *testing.T) {
	single := route.Position{Latitude: -34.6, Longitude: -58.38}
	input := route.Route{
		Stops: []route.Stop{
			{
				ID:         "A",
				Position:   single,
				PathToNext: &route.LineString{Positions: []route.Position{single}},
			},
			{ID: "B", Position: route.Position{Latitude: -34.61, Longitude: -58.39}},
			{ID: "C", Position: route.Position{Latitude: -34.62, Longitude: -58.40}},
		},
	}

	if err := AlignStoredPathEndpoints(&input, testToleranceMeters); err != nil {
		t.Fatalf("AlignStoredPathEndpoints() error = %v", err)
	}
	if len(input.Stops[0].PathToNext.Positions) != 1 {
		t.Fatalf("path = %#v, want it unchanged", input.Stops[0].PathToNext)
	}
	if input.Stops[1].PathToNext != nil {
		t.Fatalf("missing path = %#v, want nil", input.Stops[1].PathToNext)
	}
}

// storedRoute mirrors an export whose segment boundaries were projected onto
// the GTFS shape and therefore land near, but not on, each stop.
func storedRoute() route.Route {
	return route.Route{
		Jurisdiction: route.JurisdictionCABA,
		Stops: []route.Stop{
			{
				ID:       "2031665",
				Position: route.Position{Latitude: -34.586005, Longitude: -58.373625},
				PathToNext: &route.LineString{Positions: []route.Position{
					{Latitude: -34.586005, Longitude: -58.373625},
					{Latitude: -34.589460, Longitude: -58.372723},
					{Latitude: -34.589648151, Longitude: -58.372870385},
				}},
			},
			{
				ID:       "204232",
				Position: route.Position{Latitude: -34.589460, Longitude: -58.372723},
				PathToNext: &route.LineString{Positions: []route.Position{
					{Latitude: -34.589648151, Longitude: -58.372870385},
					{Latitude: -34.591970, Longitude: -58.374470},
					{Latitude: -34.592301286, Longitude: -58.374740954},
				}},
			},
			{
				ID:       "204208",
				Position: route.Position{Latitude: -34.591970, Longitude: -58.374470},
			},
		},
	}
}
