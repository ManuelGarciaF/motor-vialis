package demand

import (
	"math"
	"testing"
)

func TestLinearAccessibility(t *testing.T) {
	calculator := LinearAccessibility{}
	assertAccessibility(t, calculator, 0, 800, 1)
	assertAccessibility(t, calculator, 400, 800, 0.5)
	assertAccessibility(t, calculator, 800, 800, 0)
	assertAccessibility(t, calculator, 900, 800, 0)
}

func TestQuadraticAccessibility(t *testing.T) {
	calculator := QuadraticAccessibility{}
	assertAccessibility(t, calculator, 0, 800, 1)
	assertAccessibility(t, calculator, 400, 800, 0.25)
	assertAccessibility(t, calculator, 800, 800, 0)
	assertAccessibility(t, calculator, 900, 800, 0)
}

func assertAccessibility(
	t *testing.T,
	calculator AccessibilityCalculator,
	distanceMeters, radiusMeters, want float64,
) {
	t.Helper()
	actual := calculator.Calculate(distanceMeters, radiusMeters)
	if math.Abs(actual-want) > 1e-9 {
		t.Fatalf(
			"Calculate(%v, %v) = %v, want %v",
			distanceMeters,
			radiusMeters,
			actual,
			want,
		)
	}
}
