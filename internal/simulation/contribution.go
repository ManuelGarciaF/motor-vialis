package simulation

import (
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// stopResults aggregates estimator detail by stop while preserving route order.
func stopResults(
	input route.Route,
	demandResult demand.Result,
	travelTimeResult traveltime.Result,
	revenueResult revenue.Result,
) []StopResult {
	results := make([]StopResult, len(input.Stops))
	orderByStopID := make(map[string]int, len(input.Stops))
	for order, stop := range input.Stops {
		results[order] = StopResult{StopOrder: order, StopID: stop.ID}
		orderByStopID[stop.ID] = order
	}

	for _, pair := range demandResult.ByStopPair {
		if origin, found := orderByStopID[pair.OriginStopID]; found {
			results[origin].Demand.OriginGross += pair.GrossDemand
			results[origin].Demand.OriginPotential += pair.PotentialDemand
		}
		if destination, found := orderByStopID[pair.DestinationStopID]; found {
			results[destination].Demand.DestinationGross += pair.GrossDemand
			results[destination].Demand.DestinationPotential += pair.PotentialDemand
		}
	}

	for _, pair := range revenueResult.ByStopPair {
		if origin, found := orderByStopID[pair.OriginStopID]; found {
			results[origin].Revenue.OriginPotentialCents += pair.PotentialRevenueCents
		}
		if destination, found := orderByStopID[pair.DestinationStopID]; found {
			results[destination].Revenue.DestinationPotentialCents +=
				pair.PotentialRevenueCents
		}
	}

	// Segment order matches the originating stop order.
	for index, segment := range travelTimeResult.BySegment {
		if index >= len(results)-1 {
			break
		}
		results[index].SegmentToNext = &SegmentResult{
			DistanceMeters:      segment.DistanceMeters,
			OffPeakSeconds:      segment.OffPeakSeconds,
			TypicalSeconds:      segment.TypicalSeconds,
			PeakSeconds:         segment.PeakSeconds,
			Confidence:          segment.Confidence,
			Source:              segment.Source,
			ReferenceRouteCount: segment.ReferenceRouteCount,
		}
	}
	return results
}
