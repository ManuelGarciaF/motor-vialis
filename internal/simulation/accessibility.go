package simulation

// AccessibilityCalculator converts the distance to a cell into a coefficient
// between zero and one.
type AccessibilityCalculator interface {
	Calculate(distanceMeters, radiusMeters float64) float64
}

// LinearAccessibility calculates 1 - distance/radius.
type LinearAccessibility struct{}

func (LinearAccessibility) Calculate(distanceMeters, radiusMeters float64) float64 {
	return remainingAccessibility(distanceMeters, radiusMeters)
}

// QuadraticAccessibility calculates (1 - distance/radius)^2.
type QuadraticAccessibility struct{}

func (QuadraticAccessibility) Calculate(distanceMeters, radiusMeters float64) float64 {
	remaining := remainingAccessibility(distanceMeters, radiusMeters)
	return remaining * remaining
}

func remainingAccessibility(distanceMeters, radiusMeters float64) float64 {
	if radiusMeters <= 0 || distanceMeters >= radiusMeters {
		return 0
	}
	if distanceMeters <= 0 {
		return 1
	}
	return 1 - distanceMeters/radiusMeters
}
