// Package simulation orchestrates the independent estimators for one route.
package simulation

import (
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// Route aliases expose the shared route model through the simulation API.
type (
	Position        = route.Position
	LineString      = route.LineString
	Stop            = route.Stop
	Route           = route.Route
	ValidationError = route.ValidationError
)

// Result contains route totals and each stop's contribution.
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

// TravelTimeTotals includes the worst confidence found among its segments.
type TravelTimeTotals struct {
	OffPeakSeconds int64                 `json:"offPeakSeconds"`
	TypicalSeconds int64                 `json:"typicalSeconds"`
	PeakSeconds    int64                 `json:"peakSeconds"`
	Confidence     traveltime.Confidence `json:"confidence"`
}

// StopResult describes one stop and its optional outgoing segment.
type StopResult struct {
	StopOrder     int            `json:"stopOrder"`
	StopID        string         `json:"stopId"`
	Demand        StopDemand     `json:"demand"`
	Revenue       StopRevenue    `json:"revenue"`
	SegmentToNext *SegmentResult `json:"segmentToNext"`
}

// StopDemand separates origin and destination demand to avoid double counting.
type StopDemand struct {
	OriginGross          float64 `json:"originGross"`
	OriginPotential      float64 `json:"originPotential"`
	DestinationGross     float64 `json:"destinationGross"`
	DestinationPotential float64 `json:"destinationPotential"`
}

// StopRevenue separates revenue attributed to origins and destinations.
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
