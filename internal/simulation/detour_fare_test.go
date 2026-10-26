package simulation

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

func TestDetourFareTravelTimeChargesAlongTheBaseline(t *testing.T) {
	// Out-and-back baseline: A→B→C eastwards, then D→E→F westwards about
	// 110 m south. Every baseline segment is measured as 1000 m.
	outAndBack := baselineFixture([]fixtureStop{
		{"A", -34.6000, -58.400},
		{"B", -34.6000, -58.390},
		{"C", -34.6000, -58.380},
		{"D", -34.6010, -58.380},
		{"E", -34.6010, -58.390},
		{"F", -34.6010, -58.400},
	})
	straight := baselineFixture([]fixtureStop{
		{"A", -34.6000, -58.400},
		{"B", -34.6000, -58.390},
		{"C", -34.6000, -58.380},
		{"D", -34.6000, -58.370},
		{"E", -34.6000, -58.360},
	})

	cases := []struct {
		name     string
		baseline route.Route
		proposed []fixtureStop
		want     []float64
	}{
		{
			name:     "pure subset sums the skipped baseline segments",
			baseline: straight,
			proposed: []fixtureStop{{ID: "A"}, {ID: "C"}, {ID: "E"}},
			want:     []float64{2000, 2000},
		},
		{
			name:     "new stop between retained stops",
			baseline: straight,
			proposed: []fixtureStop{{ID: "B"}, {"N", -34.6003, -58.375}, {ID: "D"}},
			want:     []float64{1500, 500},
		},
		{
			name:     "new stop next to the return leg stays in its stretch",
			baseline: outAndBack,
			proposed: []fixtureStop{{ID: "A"}, {"N", -34.6009, -58.390}, {ID: "C"}, {ID: "F"}},
			want:     []float64{1000, 1000, 3000},
		},
		{
			name:     "new first stop",
			baseline: straight,
			proposed: []fixtureStop{{"N", -34.6002, -58.395}, {ID: "B"}, {ID: "C"}},
			want:     []float64{500, 1000},
		},
		{
			name:     "new first stop before the baseline start",
			baseline: straight,
			proposed: []fixtureStop{{"N", -34.6000, -58.405}, {ID: "B"}},
			want:     []float64{1000},
		},
		{
			name:     "new last stop",
			baseline: straight,
			proposed: []fixtureStop{{ID: "C"}, {ID: "D"}, {"N", -34.6002, -58.365}},
			want:     []float64{1000, 500},
		},
		{
			name:     "consecutive new stops never move backwards",
			baseline: straight,
			proposed: []fixtureStop{{ID: "A"}, {"N1", -34.6, -58.385}, {"N2", -34.6, -58.395}, {ID: "C"}},
			want:     []float64{1500, 0, 500},
		},
		{
			name:     "only new stops use the whole baseline",
			baseline: straight,
			proposed: []fixtureStop{{"N1", -34.6001, -58.395}, {"N2", -34.6001, -58.365}},
			want:     []float64{3000},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input := ComparisonInput{
				Baseline: testCase.baseline,
				Proposed: proposedFixture(testCase.baseline, testCase.proposed),
			}
			if err := validateDetourStops(input); err != nil {
				t.Fatalf("validateDetourStops() error = %v", err)
			}

			result, err := detourFareTravelTime(input, measuredSegments(testCase.baseline, 1000))
			if err != nil {
				t.Fatalf("detourFareTravelTime() error = %v", err)
			}

			if len(result.BySegment) != len(testCase.want) {
				t.Fatalf("segments = %d, want %d", len(result.BySegment), len(testCase.want))
			}
			for index, want := range testCase.want {
				segment := result.BySegment[index]
				// A 1 m tolerance absorbs the equirectangular projection.
				if math.Abs(segment.DistanceMeters-want) > 1 {
					t.Fatalf("segment %d (%s→%s) = %v m, want %v m",
						index, segment.OriginStopID, segment.DestinationStopID, segment.DistanceMeters, want)
				}
			}
		})
	}
}

func TestValidateDetourStopsRejectsReorderedRetainedStops(t *testing.T) {
	baseline := baselineFixture([]fixtureStop{
		{"A", -34.6000, -58.400},
		{"B", -34.6000, -58.390},
		{"C", -34.6000, -58.380},
	})

	cases := []struct {
		name      string
		proposed  []fixtureStop
		wantField string
	}{
		{
			name:      "swapped retained stops",
			proposed:  []fixtureStop{{ID: "A"}, {ID: "C"}, {ID: "B"}},
			wantField: "proposed.stops[2].id",
		},
		{
			name:      "swapped around a new stop",
			proposed:  []fixtureStop{{ID: "B"}, {"N", -34.6001, -58.395}, {ID: "A"}},
			wantField: "proposed.stops[2].id",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateDetourStops(ComparisonInput{
				Baseline: baseline,
				Proposed: proposedFixture(baseline, testCase.proposed),
			})

			var validationError *route.ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error = %v, want *route.ValidationError", err)
			}
			if validationError.Field != testCase.wantField {
				t.Fatalf("field = %q, want %q", validationError.Field, testCase.wantField)
			}
		})
	}
}

func TestCompareDetourAcceptsNewStops(t *testing.T) {
	baseline := threeStopRoute()
	proposed := proposedFixture(baseline, []fixtureStop{
		{ID: "A"},
		{"N", baseline.Stops[1].Position.Latitude, baseline.Stops[1].Position.Longitude + 0.0005},
		{ID: "C"},
	})
	maximum := int64(2500)
	service := NewService(
		detourDemandEstimator{},
		detourTravelTimeEstimator{},
		revenue.NewService(detourTariffRepository{bands: []revenue.TariffBand{
			{MinimumDistanceMeters: 0, MaximumDistanceMeters: &maximum, RegisteredFareCents: 100, UnregisteredFareCents: 100},
			{MinimumDistanceMeters: 2500, RegisteredFareCents: 200, UnregisteredFareCents: 200},
		}}, revenue.Policy{CaptureFactor: 1, RegisteredCardShare: 1}),
	)

	comparison, err := service.CompareDetour(context.Background(), ComparisonInput{
		Baseline: baseline,
		Proposed: proposed,
	})
	if err != nil {
		t.Fatalf("CompareDetour() error = %v", err)
	}
	if comparison.Delta.StopCount.Proposed != 3 {
		t.Fatalf("proposed stop count = %d, want 3", comparison.Delta.StopCount.Proposed)
	}
	if comparison.Proposed.Global.Revenue.PotentialRevenueCents != 100 {
		t.Fatalf("proposed revenue = %v, want original-distance fare 100", comparison.Proposed.Global.Revenue.PotentialRevenueCents)
	}
}

// fixtureStop is a stop in a fare test. A proposed stop with only an ID
// reuses the baseline stop with that ID.
type fixtureStop struct {
	ID        string
	Latitude  float64
	Longitude float64
}

func baselineFixture(stops []fixtureStop) route.Route {
	result := route.Route{Jurisdiction: route.JurisdictionCABA}
	for _, stop := range stops {
		result.Stops = append(result.Stops, route.Stop{
			ID:       stop.ID,
			Position: route.Position{Latitude: stop.Latitude, Longitude: stop.Longitude},
		})
	}
	linkStops(result.Stops)
	return result
}

func proposedFixture(baseline route.Route, stops []fixtureStop) route.Route {
	byID := make(map[string]route.Stop, len(baseline.Stops))
	for _, stop := range baseline.Stops {
		byID[stop.ID] = stop
	}

	result := route.Route{Jurisdiction: baseline.Jurisdiction}
	for _, stop := range stops {
		position := route.Position{Latitude: stop.Latitude, Longitude: stop.Longitude}
		if existing, found := byID[stop.ID]; found {
			position = existing.Position
		}
		result.Stops = append(result.Stops, route.Stop{ID: stop.ID, Position: position})
	}
	linkStops(result.Stops)
	return result
}

// linkStops joins consecutive stops with straight paths.
func linkStops(stops []route.Stop) {
	for index := range stops {
		stops[index].PathToNext = nil
		if index < len(stops)-1 {
			stops[index].PathToNext = &route.LineString{Positions: []route.Position{
				stops[index].Position,
				stops[index+1].Position,
			}}
		}
	}
}

func measuredSegments(baseline route.Route, distance float64) []traveltime.SegmentResult {
	segments := make([]traveltime.SegmentResult, 0, len(baseline.Stops)-1)
	for index := 0; index < len(baseline.Stops)-1; index++ {
		segments = append(segments, traveltime.SegmentResult{
			OriginStopID:      baseline.Stops[index].ID,
			DestinationStopID: baseline.Stops[index+1].ID,
			DistanceMeters:    distance,
		})
	}
	return segments
}
