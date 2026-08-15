package simulation

import (
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// StopContribution attributes route-level demand and revenue to one stop.
//
// Origin and destination figures are reported apart on purpose. Every stop pair
// contributes to its origin stop and to its destination stop, so a column
// adding both would count each trip twice; kept apart, each column sums to the
// route total. They also answer different questions: demand that begins at a
// stop and demand that ends there justify different decisions.
type StopContribution struct {
	StopOrder int    `json:"stopOrder"`
	StopID    string `json:"stopId"`

	OriginGrossDemand          float64 `json:"originGrossDemand"`
	OriginPotentialDemand      float64 `json:"originPotentialDemand"`
	DestinationGrossDemand     float64 `json:"destinationGrossDemand"`
	DestinationPotentialDemand float64 `json:"destinationPotentialDemand"`

	OriginPotentialRevenueCents      float64 `json:"originPotentialRevenueCents"`
	DestinationPotentialRevenueCents float64 `json:"destinationPotentialRevenueCents"`
}

// StopContributions aggregates the stop pairs already calculated by the demand
// and revenue estimators into one entry per stop, in route order.
//
// Stops that contribute nothing are still listed: showing that a stop adds no
// demand is exactly what makes removing it a defensible decision.
func StopContributions(
	input route.Route,
	demandResult demand.Result,
	revenueResult revenue.Result,
) []StopContribution {
	contributions := make([]StopContribution, len(input.Stops))
	orderByStopID := make(map[string]int, len(input.Stops))
	for order, stop := range input.Stops {
		contributions[order] = StopContribution{StopOrder: order, StopID: stop.ID}
		orderByStopID[stop.ID] = order
	}

	for _, pair := range demandResult.ByStopPair {
		if origin, found := orderByStopID[pair.OriginStopID]; found {
			contributions[origin].OriginGrossDemand += pair.GrossDemand
			contributions[origin].OriginPotentialDemand += pair.PotentialDemand
		}
		if destination, found := orderByStopID[pair.DestinationStopID]; found {
			contributions[destination].DestinationGrossDemand += pair.GrossDemand
			contributions[destination].DestinationPotentialDemand += pair.PotentialDemand
		}
	}

	// Revenue pairs carry stop identifiers but no order, so they are matched by
	// identifier; route.Validate has already guaranteed those are unique.
	for _, pair := range revenueResult.ByStopPair {
		if origin, found := orderByStopID[pair.OriginStopID]; found {
			contributions[origin].OriginPotentialRevenueCents += pair.PotentialRevenueCents
		}
		if destination, found := orderByStopID[pair.DestinationStopID]; found {
			contributions[destination].DestinationPotentialRevenueCents +=
				pair.PotentialRevenueCents
		}
	}
	return contributions
}
