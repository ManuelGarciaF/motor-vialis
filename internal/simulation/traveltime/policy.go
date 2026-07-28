package traveltime

// Policy controls how local GTFS references are selected.
type Policy struct {
	ReferenceRadiiMeters      []float64
	MinimumReferenceRoutes    int
	DirectionToleranceDegrees float64
	MinimumCommercialSpeedKPH float64
	MaximumCommercialSpeedKPH float64
}

// DefaultPolicy returns the deterministic policy used by the simulation.
func DefaultPolicy() Policy {
	return Policy{
		ReferenceRadiiMeters:      []float64{100, 300, 800},
		MinimumReferenceRoutes:    3,
		DirectionToleranceDegrees: 60,
		MinimumCommercialSpeedKPH: 2,
		MaximumCommercialSpeedKPH: 80,
	}
}
