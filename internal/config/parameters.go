package config

import (
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// Model parameters are constants so output-changing decisions remain reviewable.
// Deployment settings and credentials are read from the environment in config.go.

const (
	// AccessRadiusMeters limits which demand cells may be assigned to a stop.
	AccessRadiusMeters = 800.0

	// RevenueCaptureFactor is the share of potential demand expected to board.
	RevenueCaptureFactor = 1.0

	// RegisteredCardShare selects the lower registered-SUBE tariff proportion.
	RegisteredCardShare = 1.0
)

// SimulationTimeout must remain below WriteTimeout to allow an HTTP timeout response.
const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 10 * time.Second
	WriteTimeout      = 30 * time.Second
	IdleTimeout       = 60 * time.Second
	SimulationTimeout = 25 * time.Second

	// DatabaseConnectTimeout bounds connectivity checks and graceful shutdown.
	DatabaseConnectTimeout = 10 * time.Second
)

// AccessibilityCalculator returns the model's distance-weighting strategy.
func AccessibilityCalculator() demand.AccessibilityCalculator {
	return demand.LinearAccessibility{}
}

// Detour model limits determine which route variants are operable.
const (
	DetourForbiddenCorridorMeters          = 5.0
	DetourForcedStopRadiusMeters           = 20.0
	DetourOptionalStopRadiusMeters         = 500.0
	DetourSearchRadiusMeters               = 1000.0
	DetourMaximumCutPositions              = 10_000
	DetourMaximumCutLengthMeters           = 20_000.0
	DetourMaximumTrafficTiles              = 32
	DetourTrafficZoom                      = 14
	DetourTrafficMatchRadiusMeters         = 15.0
	DetourTrafficDirectionToleranceDegrees = 45.0
	DetourTrafficEstimateRadiusMeters      = 300.0
	DetourTrafficEstimateMinimumSamples    = 3
	DetourTrafficEstimateMaximumSamples    = 5
	DetourPointDirectionToleranceDegrees   = 60.0

	TomTomTrafficTTL          = 30 * time.Minute
	TomTomRequestTimeout      = 10 * time.Second
	TomTomRequestsPerSecond   = 10
	TomTomCacheEntries        = 256
	TomTomCacheBytes          = 16 << 20
	TomTomMaximumTileBytes    = 20 << 20
	TomTomMaximumTileFeatures = 100_000
	TomTomTileMargin          = 0.1
)

// DetourPolicy returns the geographic thresholds and defensive limits used by
// RF05.
func DetourPolicy() detour.Policy {
	return detour.Policy{
		ForbiddenCorridorMeters:          DetourForbiddenCorridorMeters,
		ForcedStopRadiusMeters:           DetourForcedStopRadiusMeters,
		OptionalStopRadiusMeters:         DetourOptionalStopRadiusMeters,
		SearchRadiusMeters:               DetourSearchRadiusMeters,
		MaximumCutPositions:              DetourMaximumCutPositions,
		MaximumCutLengthMeters:           DetourMaximumCutLengthMeters,
		MaximumTrafficTiles:              DetourMaximumTrafficTiles,
		TrafficZoom:                      DetourTrafficZoom,
		TrafficMatchRadiusMeters:         DetourTrafficMatchRadiusMeters,
		TrafficDirectionToleranceDegrees: DetourTrafficDirectionToleranceDegrees,
		TrafficEstimateRadiusMeters:      DetourTrafficEstimateRadiusMeters,
		TrafficEstimateMinimumSamples:    DetourTrafficEstimateMinimumSamples,
		TrafficEstimateMaximumSamples:    DetourTrafficEstimateMaximumSamples,
		PointDirectionToleranceDegrees:   DetourPointDirectionToleranceDegrees,
	}
}

// TravelTimePolicy defines corridor selection and valid commercial-speed bounds.
func TravelTimePolicy() traveltime.Policy {
	return traveltime.Policy{
		ReferenceRadiiMeters:      []float64{100, 300, 800},
		MinimumReferenceRoutes:    3,
		DirectionToleranceDegrees: 60,
		MinimumCommercialSpeedKPH: 2,
		MaximumCommercialSpeedKPH: 80,
	}
}

// Stored-line export parameters.
const (
	// LinesAlignmentToleranceMeters applies only to stored GTFS geometry.
	LinesAlignmentToleranceMeters = 250.0

	// LinesDefaultPageSize applies when the caller omits a limit.
	LinesDefaultPageSize = 50

	// LinesMaximumPageSize caps one response and must not be below the default.
	LinesMaximumPageSize = 200
)

// Finding the stored lines that share a corridor with a route someone drew,
// for POST /lines/similar.
const (
	// SimilarityCorridorToleranceMeters is how far a stored line may run from
	// the drawn route and still count as the same corridor. Roughly one block
	// of the AMBA grid: a line coming down the parallel street is still
	// recognisably the same corridor to a passenger, who walks to the corner,
	// while one two blocks away is a different service.
	//
	// It is close to LinesAlignmentToleranceMeters (250 m) by coincidence, not
	// by kinship, and the two must move independently. That one asks whether a
	// stored path endpoint is the same *place* as its stop; this one asks
	// whether two whole lines serve the same *corridor*. Widening this one
	// because the feed's geometry got sloppier — or the other because a wider
	// corridor seemed useful — would change a question nobody meant to ask.
	SimilarityCorridorToleranceMeters = 200.0

	// SimilarityMinimumCoverage is the share of one of the two lines that has
	// to fall inside the other's corridor before the pair is reported at all.
	// Below it the two merely touch: every line crossing an avenue picks up a
	// few percent of overlap, and reporting those would bury the handful of
	// lines that actually run alongside the proposal.
	SimilarityMinimumCoverage = 0.20

	// SimilarityDefaultResultCount is how many matches a corridor search
	// returns when the caller does not ask for a number, and
	// SimilarityMaximumResultCount caps what it may ask for. The search is a
	// shortlist to choose a baseline from, not a listing to page through, so
	// both are far smaller than the page sizes above.
	// TestSimilarityPolicyIsCoherent enforces that the default fits under the
	// maximum.
	SimilarityDefaultResultCount = 10
	SimilarityMaximumResultCount = 50
)

// LinesPolicy returns the rules applied when exporting stored lines and when
// matching a drawn route against them.
func LinesPolicy() lines.Policy {
	return lines.Policy{
		AlignmentToleranceMeters:          LinesAlignmentToleranceMeters,
		DefaultPageSize:                   LinesDefaultPageSize,
		MaximumPageSize:                   LinesMaximumPageSize,
		SimilarityCorridorToleranceMeters: SimilarityCorridorToleranceMeters,
		SimilarityMinimumCoverage:         SimilarityMinimumCoverage,
		SimilarityDefaultResultCount:      SimilarityDefaultResultCount,
		SimilarityMaximumResultCount:      SimilarityMaximumResultCount,
	}
}

// Reading the ranking of frequent line combinations, for GET /transfers.
//
// Two numbers that shape that ranking are deliberately not here: the 800 m
// between a cell's point of maximum concurrence and a stop, and the 300 m a
// passenger walks to change buses. Both are applied by the ETL that builds the
// ranking (sql/viajes/combinaciones_lineas.sql and
// sql/recorridos/conexiones_recorridos.sql), so by the time a request arrives
// they are already baked into the stored rows. Declaring them here would
// suggest a request could change them, and changing one means rebuilding the
// aggregate, not restarting the process.
const (
	// TransfersDefaultPageSize is how many combinations a page returns when
	// the caller does not ask for a size, and TransfersMaximumPageSize caps
	// what it may ask for. Ten is what fits a decision: each row is a
	// candidate for a new direct line, not a record to skim.
	// TestTransfersPolicyIsCoherent enforces that the default fits under the
	// maximum.
	TransfersDefaultPageSize = 10
	TransfersMaximumPageSize = 50

	// TransfersWeakEvidenceAlternatives is the average number of feasible
	// combinations above which the engine stops asserting and starts warning.
	//
	// The ranking splits every flow equally among the combinations that could
	// have served it, so this is really a floor on how much of a flow lands on
	// one pair: above 5, a pair carries under a fifth of the flows behind its
	// own number, and calling that "the combination people make" would be
	// reading a split as a measurement.
	//
	// It sits at half of the ETL's ceiling of 10 feasible combinations
	// (sql/viajes/combinaciones_lineas.sql) on purpose: a threshold at or
	// above the ceiling would never fire, and one far below it would fire on
	// everything. Both turn the mark into decoration. Half splits the surviving
	// range, so the mark discriminates.
	//
	// It is the one parameter here with no principled derivation — it is a
	// judgement about when a claim gets thin — so it moves together with that
	// ceiling rather than on its own.
	TransfersWeakEvidenceAlternatives = 5.0
)

// CombinacionesPolicy returns the rules applied when reading the ranking of
// frequent line combinations.
func CombinacionesPolicy() combinaciones.Policy {
	return combinaciones.Policy{
		DefaultPageSize:          TransfersDefaultPageSize,
		MaximumPageSize:          TransfersMaximumPageSize,
		WeakEvidenceAlternatives: TransfersWeakEvidenceAlternatives,
	}
}

// RevenuePolicy turns potential demand into potential revenue.
func RevenuePolicy() revenue.Policy {
	return revenue.Policy{
		CaptureFactor:       RevenueCaptureFactor,
		RegisteredCardShare: RegisteredCardShare,
	}
}
