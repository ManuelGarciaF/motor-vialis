package config

import (
	"time"

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

// LinesPolicy returns the rules applied when exporting stored lines.
func LinesPolicy() lines.Policy {
	return lines.Policy{
		AlignmentToleranceMeters: LinesAlignmentToleranceMeters,
		DefaultPageSize:          LinesDefaultPageSize,
		MaximumPageSize:          LinesMaximumPageSize,
	}
}

// RevenuePolicy turns potential demand into potential revenue.
func RevenuePolicy() revenue.Policy {
	return revenue.Policy{
		CaptureFactor:       RevenueCaptureFactor,
		RegisteredCardShare: RegisteredCardShare,
	}
}
