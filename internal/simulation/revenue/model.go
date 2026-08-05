// Package revenue calculates potential fare revenue for a simulated route.
package revenue

import "github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"

// TariffBand is one jurisdictional fare tier. Amounts are stored in cents to
// avoid monetary rounding errors in the tariff source of truth.
type TariffBand struct {
	MinimumDistanceMeters int64
	MaximumDistanceMeters *int64
	RegisteredFareCents   int64
	UnregisteredFareCents int64
}

// StopPairResult explains the revenue calculation for one ordered stop pair.
type StopPairResult struct {
	OriginStopID          string  `json:"originStopId"`
	DestinationStopID     string  `json:"destinationStopId"`
	DistanceMeters        float64 `json:"distanceMeters"`
	PotentialDemand       float64 `json:"potentialDemand"`
	CapturedDemand        float64 `json:"capturedDemand"`
	RegisteredFareCents   int64   `json:"registeredFareCents"`
	UnregisteredFareCents int64   `json:"unregisteredFareCents"`
	WeightedFareCents     float64 `json:"weightedFareCents"`
	PotentialRevenueCents float64 `json:"potentialRevenueCents"`
}

// Result contains the revenue expected from potential demand on a typical day.
type Result struct {
	Jurisdiction          route.Jurisdiction `json:"jurisdiction"`
	CaptureFactor         float64            `json:"captureFactor"`
	RegisteredCardShare   float64            `json:"registeredCardShare"`
	PotentialRevenueCents float64            `json:"potentialRevenueCents"`
	ByStopPair            []StopPairResult   `json:"byStopPair"`
}
