package detour

// Policy contains versioned model limits and geographic thresholds. Distances
// are metres and boundaries are inclusive unless the field says otherwise.
type Policy struct {
	ForbiddenCorridorMeters  float64
	ForcedStopRadiusMeters   float64
	OptionalStopRadiusMeters float64
	SearchRadiusMeters       float64
	MaximumCutPositions      int
	MaximumCutLengthMeters   float64
	MaximumTrafficTiles      int
	TrafficZoom              int
}

func (policy Policy) validate() error {
	if policy.ForbiddenCorridorMeters <= 0 ||
		policy.ForcedStopRadiusMeters <= 0 ||
		policy.OptionalStopRadiusMeters < policy.ForcedStopRadiusMeters ||
		policy.SearchRadiusMeters < policy.OptionalStopRadiusMeters ||
		policy.MaximumCutPositions < 2 ||
		policy.MaximumCutLengthMeters <= 0 ||
		policy.MaximumTrafficTiles <= 0 ||
		policy.TrafficZoom < 0 {
		return &ValidationError{
			Field:   "policy",
			Message: "contains incoherent detour limits",
		}
	}
	return nil
}
