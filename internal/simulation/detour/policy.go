package detour

// Policy contains versioned model limits and geographic thresholds. Distances
// are metres and boundaries are inclusive unless the field says otherwise.
type Policy struct {
	ForbiddenCorridorMeters          float64
	ForcedStopRadiusMeters           float64
	OptionalStopRadiusMeters         float64
	SearchRadiusMeters               float64
	MaximumCutPositions              int
	MaximumCutLengthMeters           float64
	MaximumTrafficTiles              int
	TrafficZoom                      int
	TrafficMatchRadiusMeters         float64
	TrafficDirectionToleranceDegrees float64
	TrafficEstimateRadiusMeters      float64
	TrafficEstimateMinimumSamples    int
	TrafficEstimateMaximumSamples    int
	PointDirectionToleranceDegrees   float64
}

func (policy Policy) validate() error {
	if policy.ForbiddenCorridorMeters <= 0 ||
		policy.ForcedStopRadiusMeters <= 0 ||
		policy.OptionalStopRadiusMeters < policy.ForcedStopRadiusMeters ||
		policy.SearchRadiusMeters < policy.OptionalStopRadiusMeters ||
		policy.MaximumCutPositions < 2 ||
		policy.MaximumCutLengthMeters <= 0 ||
		policy.MaximumTrafficTiles <= 0 ||
		policy.TrafficZoom < 0 ||
		policy.TrafficMatchRadiusMeters <= 0 ||
		policy.TrafficDirectionToleranceDegrees <= 0 ||
		policy.TrafficEstimateRadiusMeters <= 0 ||
		policy.TrafficEstimateMinimumSamples <= 0 ||
		policy.TrafficEstimateMaximumSamples < policy.TrafficEstimateMinimumSamples ||
		policy.PointDirectionToleranceDegrees <= 0 {
		return &ValidationError{
			Field:   "policy",
			Message: "contains incoherent detour limits",
		}
	}
	return nil
}
