package traveltime

// Policy controls how local GTFS references are selected.
type Policy struct {
	ReferenceRadiiMeters      []float64
	MinimumReferenceRoutes    int
	DirectionToleranceDegrees float64
	MinimumCommercialSpeedKPH float64
	MaximumCommercialSpeedKPH float64
}
