package config

import (
	"time"

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
