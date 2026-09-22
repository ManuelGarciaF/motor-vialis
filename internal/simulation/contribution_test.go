package simulation

import (
	"math"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// Origin and destination columns must each reconcile with route totals.
func TestStopResultsSumToTheRouteTotals(t *testing.T) {
	input, demandResult, travelTimeResult, revenueResult := stopResultFixture()

	results := stopResults(input, demandResult, travelTimeResult, revenueResult)

	var originDemand, destinationDemand float64
	var originGross, destinationGross float64
	var originRevenue, destinationRevenue float64
	for _, result := range results {
		originDemand += result.Demand.OriginPotential
		destinationDemand += result.Demand.DestinationPotential
		originGross += result.Demand.OriginGross
		destinationGross += result.Demand.DestinationGross
		originRevenue += result.Revenue.OriginPotentialCents
		destinationRevenue += result.Revenue.DestinationPotentialCents
	}

	totals := []struct {
		name          string
		actual, total float64
	}{
		{"origin potential demand", originDemand, demandResult.PotentialDemand},
		{"destination potential demand", destinationDemand, demandResult.PotentialDemand},
		{"origin gross demand", originGross, demandResult.GrossDemand},
		{"destination gross demand", destinationGross, demandResult.GrossDemand},
		{"origin revenue", originRevenue, revenueResult.PotentialRevenueCents},
		{"destination revenue", destinationRevenue, revenueResult.PotentialRevenueCents},
	}
	for _, total := range totals {
		if math.Abs(total.actual-total.total) > 1e-9 {
			t.Fatalf("%s = %v, want %v", total.name, total.actual, total.total)
		}
	}
}

func TestStopResultsListEveryStopInRouteOrder(t *testing.T) {
	input, demandResult, travelTimeResult, revenueResult := stopResultFixture()

	results := stopResults(input, demandResult, travelTimeResult, revenueResult)

	if len(results) != len(input.Stops) {
		t.Fatalf("results = %d, want %d", len(results), len(input.Stops))
	}
	for order, result := range results {
		if result.StopOrder != order || result.StopID != input.Stops[order].ID {
			t.Fatalf("results[%d] = %#v", order, result)
		}
	}
}

func TestStopResultsAttachEachSegmentToItsOriginStop(t *testing.T) {
	input, demandResult, travelTimeResult, revenueResult := stopResultFixture()

	results := stopResults(input, demandResult, travelTimeResult, revenueResult)

	if results[0].SegmentToNext == nil || results[1].SegmentToNext == nil {
		t.Fatalf("results = %#v, want a segment on every stop but the last", results)
	}
	if results[0].SegmentToNext.TypicalSeconds != 120 ||
		results[0].SegmentToNext.Source != traveltime.SourceLocal100 {
		t.Fatalf("first segment = %#v", results[0].SegmentToNext)
	}
	if results[1].SegmentToNext.TypicalSeconds != 240 {
		t.Fatalf("second segment = %#v", results[1].SegmentToNext)
	}
	if last := results[len(results)-1]; last.SegmentToNext != nil {
		t.Fatalf("last stop segment = %#v, want nil", last.SegmentToNext)
	}
}

// Stops with no contribution remain visible with zero values.
func TestStopResultsKeepStopsThatContributeNothing(t *testing.T) {
	input, demandResult, travelTimeResult, revenueResult := stopResultFixture()

	results := stopResults(input, demandResult, travelTimeResult, revenueResult)

	first := results[0]
	if first.Demand.DestinationPotential != 0 ||
		first.Revenue.DestinationPotentialCents != 0 {
		t.Fatalf("first stop = %#v, want no destination contribution", first)
	}
	last := results[len(results)-1]
	if last.Demand.OriginPotential != 0 || last.Revenue.OriginPotentialCents != 0 {
		t.Fatalf("last stop = %#v, want no origin contribution", last)
	}
}

func TestStopResultsIgnoreUnknownStopIdentifiers(t *testing.T) {
	input, demandResult, travelTimeResult, revenueResult := stopResultFixture()
	demandResult.ByStopPair = append(demandResult.ByStopPair, demand.StopPairDemand{
		OriginStopID:      "missing",
		DestinationStopID: "also-missing",
		GrossDemand:       999,
		PotentialDemand:   999,
	})

	results := stopResults(input, demandResult, travelTimeResult, revenueResult)

	var total float64
	for _, result := range results {
		total += result.Demand.OriginPotential
	}
	if math.Abs(total-demandResult.PotentialDemand) > 1e-9 {
		t.Fatalf("origin potential demand = %v, want %v", total, demandResult.PotentialDemand)
	}
}

func stopResultFixture() (
	route.Route,
	demand.Result,
	traveltime.Result,
	revenue.Result,
) {
	input := route.Route{
		Jurisdiction: route.JurisdictionCABA,
		Stops: []route.Stop{
			{ID: "A"},
			{ID: "B"},
			{ID: "C"},
		},
	}
	demandResult := demand.Result{
		GrossDemand:     180,
		PotentialDemand: 90,
		ByStopPair: []demand.StopPairDemand{
			{
				OriginStopOrder:      0,
				OriginStopID:         "A",
				DestinationStopOrder: 1,
				DestinationStopID:    "B",
				GrossDemand:          100,
				PotentialDemand:      50,
			},
			{
				OriginStopOrder:      0,
				OriginStopID:         "A",
				DestinationStopOrder: 2,
				DestinationStopID:    "C",
				GrossDemand:          50,
				PotentialDemand:      25,
			},
			{
				OriginStopOrder:      1,
				OriginStopID:         "B",
				DestinationStopOrder: 2,
				DestinationStopID:    "C",
				GrossDemand:          30,
				PotentialDemand:      15,
			},
		},
	}
	travelTimeResult := traveltime.Result{
		TotalDistanceMeters: 1500,
		TypicalSeconds:      360,
		Confidence:          traveltime.ConfidenceHigh,
		BySegment: []traveltime.SegmentResult{
			{
				OriginStopID:      "A",
				DestinationStopID: "B",
				DistanceMeters:    1000,
				TypicalSeconds:    120,
				Confidence:        traveltime.ConfidenceHigh,
				Source:            traveltime.SourceLocal100,
			},
			{
				OriginStopID:      "B",
				DestinationStopID: "C",
				DistanceMeters:    500,
				TypicalSeconds:    240,
				Confidence:        traveltime.ConfidenceMedium,
				Source:            traveltime.SourceLocal300,
			},
		},
	}
	revenueResult := revenue.Result{
		Jurisdiction:          route.JurisdictionCABA,
		PotentialRevenueCents: 6000,
		ByStopPair: []revenue.StopPairResult{
			{OriginStopID: "A", DestinationStopID: "B", PotentialRevenueCents: 3000},
			{OriginStopID: "A", DestinationStopID: "C", PotentialRevenueCents: 2000},
			{OriginStopID: "B", DestinationStopID: "C", PotentialRevenueCents: 1000},
		},
	}
	return input, demandResult, travelTimeResult, revenueResult
}
