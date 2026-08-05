package config

import "github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"

// DefaultTravelTimePolicy returns the default policy used by the simulation.
func DefaultTravelTimePolicy() traveltime.Policy {
	return traveltime.Policy{
		ReferenceRadiiMeters:      []float64{100, 300, 800},
		MinimumReferenceRoutes:    3,
		DirectionToleranceDegrees: 60,
		MinimumCommercialSpeedKPH: 2,
		MaximumCommercialSpeedKPH: 80,
	}
}
