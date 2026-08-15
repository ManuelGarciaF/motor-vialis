package simulation

import (
	"math"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// Each column has to reconcile with the route total on its own: that is what
// makes reporting origin and destination separately meaningful.
func TestStopContributionsSumToTheRouteTotals(t *testing.T) {
	input, demandResult, revenueResult := contributionFixture()

	contributions := StopContributions(input, demandResult, revenueResult)

	var originDemand, destinationDemand float64
	var originGross, destinationGross float64
	var originRevenue, destinationRevenue float64
	for _, contribution := range contributions {
		originDemand += contribution.OriginPotentialDemand
		destinationDemand += contribution.DestinationPotentialDemand
		originGross += contribution.OriginGrossDemand
		destinationGross += contribution.DestinationGrossDemand
		originRevenue += contribution.OriginPotentialRevenueCents
		destinationRevenue += contribution.DestinationPotentialRevenueCents
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

func TestStopContributionsListEveryStopInRouteOrder(t *testing.T) {
	input, demandResult, revenueResult := contributionFixture()

	contributions := StopContributions(input, demandResult, revenueResult)

	if len(contributions) != len(input.Stops) {
		t.Fatalf("contributions = %d, want %d", len(contributions), len(input.Stops))
	}
	for order, contribution := range contributions {
		if contribution.StopOrder != order ||
			contribution.StopID != input.Stops[order].ID {
			t.Fatalf("contributions[%d] = %#v", order, contribution)
		}
	}
}

// The last stop originates nothing and the first receives nothing. Listing them
// with zeros is what shows a stop is not carrying its weight.
func TestStopContributionsKeepStopsThatContributeNothing(t *testing.T) {
	input, demandResult, revenueResult := contributionFixture()

	contributions := StopContributions(input, demandResult, revenueResult)

	first := contributions[0]
	if first.DestinationPotentialDemand != 0 ||
		first.DestinationPotentialRevenueCents != 0 {
		t.Fatalf("first stop = %#v, want no destination contribution", first)
	}
	last := contributions[len(contributions)-1]
	if last.OriginPotentialDemand != 0 || last.OriginPotentialRevenueCents != 0 {
		t.Fatalf("last stop = %#v, want no origin contribution", last)
	}
}

func TestStopContributionsIgnoreUnknownStopIdentifiers(t *testing.T) {
	input, demandResult, revenueResult := contributionFixture()
	demandResult.ByStopPair = append(demandResult.ByStopPair, demand.StopPairDemand{
		OriginStopID:      "missing",
		DestinationStopID: "also-missing",
		GrossDemand:       999,
		PotentialDemand:   999,
	})

	contributions := StopContributions(input, demandResult, revenueResult)

	var total float64
	for _, contribution := range contributions {
		total += contribution.OriginPotentialDemand
	}
	if math.Abs(total-demandResult.PotentialDemand) > 1e-9 {
		t.Fatalf("origin potential demand = %v, want %v", total, demandResult.PotentialDemand)
	}
}

func contributionFixture() (route.Route, demand.Result, revenue.Result) {
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
	revenueResult := revenue.Result{
		Jurisdiction:          route.JurisdictionCABA,
		PotentialRevenueCents: 6000,
		ByStopPair: []revenue.StopPairResult{
			{OriginStopID: "A", DestinationStopID: "B", PotentialRevenueCents: 3000},
			{OriginStopID: "A", DestinationStopID: "C", PotentialRevenueCents: 2000},
			{OriginStopID: "B", DestinationStopID: "C", PotentialRevenueCents: 1000},
		},
	}
	return input, demandResult, revenueResult
}
