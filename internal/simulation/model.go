// Package simulation orchestrates the independent estimators for one route.
package simulation

import (
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// Route input aliases preserve the concise simulation API while their shared
// implementation lives in the route package.
type (
	Position        = route.Position
	LineString      = route.LineString
	Stop            = route.Stop
	Route           = route.Route
	ValidationError = route.ValidationError
)

// Result is what the engine answers for one route.
//
// The estimators calculate far more detail than this: demand and revenue are
// derived from every origin-destination pair, which grows with the square of
// the stop count. That detail stays inside the engine. What callers need to
// decide whether a route is worth running is the route as a whole and each
// stop's part in it, so the answer has exactly those two axes.
type Result struct {
	Global GlobalResult `json:"global"`
	ByStop []StopResult `json:"byStop"`
}

// GlobalResult holds the totals for the whole route.
type GlobalResult struct {
	Demand  DemandTotals  `json:"demand"`
	Revenue RevenueTotals `json:"revenue"`
	Metrics MetricsTotals `json:"metrics"`
}

// DemandTotals is the demand the route could carry on a typical day.
type DemandTotals struct {
	GrossDemand     float64 `json:"grossDemand"`
	PotentialDemand float64 `json:"potentialDemand"`
}

// RevenueTotals is the fare revenue expected from that demand, together with
// the policy that produced it.
type RevenueTotals struct {
	Jurisdiction          route.Jurisdiction `json:"jurisdiction"`
	CaptureFactor         float64            `json:"captureFactor"`
	RegisteredCardShare   float64            `json:"registeredCardShare"`
	PotentialRevenueCents float64            `json:"potentialRevenueCents"`
}

// MetricsTotals is the measured length of the route and how long running it
// takes end to end.
type MetricsTotals struct {
	TotalDistanceMeters float64          `json:"totalDistanceMeters"`
	TravelTime          TravelTimeTotals `json:"travelTime"`
}

// TravelTimeTotals is the commercial time for the full route under each GTFS
// scenario. Confidence is the worst of any segment: a route is only as
// trustworthy as its least supported part.
type TravelTimeTotals struct {
	OffPeakSeconds int64                 `json:"offPeakSeconds"`
	TypicalSeconds int64                 `json:"typicalSeconds"`
	PeakSeconds    int64                 `json:"peakSeconds"`
	Confidence     traveltime.Confidence `json:"confidence"`
}

// StopResult is one stop's part in the route.
//
// SegmentToNext describes the ride from this stop to the following one, so it
// is null on the last stop, mirroring the pathToNext of the route that was
// submitted.
type StopResult struct {
	StopOrder     int            `json:"stopOrder"`
	StopID        string         `json:"stopId"`
	Demand        StopDemand     `json:"demand"`
	Revenue       StopRevenue    `json:"revenue"`
	SegmentToNext *SegmentResult `json:"segmentToNext"`
}

// StopDemand is the demand attributed to one stop.
//
// Demand beginning at the stop and demand ending there are reported apart
// because every origin-destination pair contributes to both of its stops:
// adding the two would count each trip twice, while each on its own sums to
// the route total. They also answer different questions, since a stop people
// leave from and a stop people arrive at justify different decisions.
type StopDemand struct {
	OriginGross          float64 `json:"originGross"`
	OriginPotential      float64 `json:"originPotential"`
	DestinationGross     float64 `json:"destinationGross"`
	DestinationPotential float64 `json:"destinationPotential"`
}

// StopRevenue is the fare revenue attributed to one stop, split the same way
// and for the same reason as StopDemand.
type StopRevenue struct {
	OriginPotentialCents      float64 `json:"originPotentialCents"`
	DestinationPotentialCents float64 `json:"destinationPotentialCents"`
}

// SegmentResult is the ride between two consecutive stops.
type SegmentResult struct {
	DistanceMeters      float64                    `json:"distanceMeters"`
	OffPeakSeconds      int64                      `json:"offPeakSeconds"`
	TypicalSeconds      int64                      `json:"typicalSeconds"`
	PeakSeconds         int64                      `json:"peakSeconds"`
	Confidence          traveltime.Confidence      `json:"confidence"`
	Source              traveltime.ReferenceSource `json:"source"`
	ReferenceRouteCount int                        `json:"referenceRouteCount"`
}
