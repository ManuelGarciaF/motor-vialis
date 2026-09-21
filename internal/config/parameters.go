package config

import (
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// This file holds the parameters of the simulation model. They are constants on
// purpose: each one changes the numbers the engine reports, so changing one is a
// change to the model that belongs in a reviewable commit, not in the
// environment of whoever happens to start the process. Only the two settings a
// deployment genuinely owns — the database URL and the listen address — are read
// from the environment, in config.go.
//
// One model constant lives next to the code that enforces it rather than here,
// because the domain packages must not depend on this one:
// route.endpointToleranceMeters (20 m, how far a pathToNext endpoint may sit
// from its stop).

const (
	// AccessRadiusMeters is the maximum walking distance between a stop and a
	// cell's point of maximum concurrence. Cells farther than this are not
	// assigned to the stop at all.
	AccessRadiusMeters = 800.0

	// RevenueCaptureFactor is the share of potential demand assumed to actually
	// board the line. At 1 the engine reports the ceiling, which is what a
	// proposal should be judged against before any operational discount.
	RevenueCaptureFactor = 1.0

	// RegisteredCardShare is the share of trips paid with a registered SUBE
	// card, which is charged the lower band of the tariff table.
	RegisteredCardShare = 1.0
)

// HTTP server timeouts. SimulationTimeout sits below WriteTimeout so a slow
// simulation is answered with a timeout status instead of having its connection
// closed mid-response. Raise both together when comparing long suburban routes.
const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 10 * time.Second
	WriteTimeout      = 30 * time.Second
	IdleTimeout       = 60 * time.Second
	SimulationTimeout = 25 * time.Second

	// DatabaseConnectTimeout bounds the startup connectivity check and the
	// graceful shutdown that mirrors it.
	DatabaseConnectTimeout = 10 * time.Second
)

// AccessibilityCalculator converts the distance between a stop and a cell into a
// 0-1 weight. demand.QuadraticAccessibility is the documented alternative: it
// applies the same ratio squared, penalising distant cells harder.
func AccessibilityCalculator() demand.AccessibilityCalculator {
	return demand.LinearAccessibility{}
}

// TravelTimePolicy controls how existing GTFS routes are chosen as a reference
// for each segment: the corridors tried in order, how many routes a corridor
// needs before it wins, and the bounds outside which a reference's commercial
// speed is discarded as a data error.
func TravelTimePolicy() traveltime.Policy {
	return traveltime.Policy{
		ReferenceRadiiMeters:      []float64{100, 300, 800},
		MinimumReferenceRoutes:    3,
		DirectionToleranceDegrees: 60,
		MinimumCommercialSpeedKPH: 2,
		MaximumCommercialSpeedKPH: 80,
	}
}

// Reading the stored GTFS lines back out, for GET /lines and /lines/{id}.
const (
	// LinesAlignmentToleranceMeters is deliberately far wider than the 20 m
	// route.Validate demands: it decides whether a stored path endpoint is
	// recognisably the same place as its stop, not whether a caller sent
	// coherent geometry. It applies only to the engine's own stored geometry,
	// never to caller input. Raise it only if the GTFS feed places shape
	// boundaries further from its stops.
	LinesAlignmentToleranceMeters = 250.0

	// LinesDefaultPageSize is how many lines a listing returns when the caller
	// does not ask for a size.
	LinesDefaultPageSize = 50

	// LinesMaximumPageSize bounds one response so listing the AMBA feed cannot
	// be turned into a full table dump by a single request. It must stay at or
	// above LinesDefaultPageSize, or a listing would silently return fewer
	// lines than configured; TestLinesPolicyIsCoherent enforces that.
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
