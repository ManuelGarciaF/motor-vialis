package detour

import (
	"fmt"
	"math"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

const earthRadiusMeters = 6371008.8

// ValidateCut checks every limit that does not require the road-network
// database. Graph coverage is checked separately through Repository.
func ValidateCut(cut Cut, policy Policy) error {
	if err := policy.validate(); err != nil {
		return err
	}
	positions := cut.LineString.Positions
	if len(positions) < 2 {
		return invalidCut("cut.coordinates must contain at least two positions")
	}
	if len(positions) > policy.MaximumCutPositions {
		return invalidCut(fmt.Sprintf(
			"cut.coordinates must contain at most %d positions",
			policy.MaximumCutPositions,
		))
	}

	var lengthMeters float64
	for index, position := range positions {
		if !finite(position.Latitude) || position.Latitude < -90 || position.Latitude > 90 {
			return invalidCut(fmt.Sprintf(
				"cut.coordinates[%d].latitude must be between -90 and 90",
				index,
			))
		}
		if !finite(position.Longitude) || position.Longitude < -180 || position.Longitude > 180 {
			return invalidCut(fmt.Sprintf(
				"cut.coordinates[%d].longitude must be between -180 and 180",
				index,
			))
		}
		if index == 0 {
			continue
		}

		segmentLength := route.DistanceMeters(positions[index-1], position)
		if segmentLength == 0 {
			return invalidCut(fmt.Sprintf(
				"cut.coordinates[%d] must differ from the previous position",
				index,
			))
		}
		lengthMeters += segmentLength
		if lengthMeters > policy.MaximumCutLengthMeters {
			return invalidCut(fmt.Sprintf(
				"cut length must not exceed %.0f meters",
				policy.MaximumCutLengthMeters,
			))
		}
	}
	return nil
}

// ClassifyStops applies the forced, optional, and required stop radii to an
// already validated cut. Distance is measured to the closest great-circle
// segment rather than to its vertices.
func ClassifyStops(
	input route.Route,
	cut Cut,
	policy Policy,
) []ClassifiedStop {
	result := make([]ClassifiedStop, len(input.Stops))
	for index, stop := range input.Stops {
		distance := distanceToLineString(stop.Position, cut.LineString)
		availability := StopRequired
		switch {
		case distance <= policy.ForcedStopRadiusMeters:
			availability = StopForcedUnavailable
		case distance <= policy.OptionalStopRadiusMeters:
			availability = StopOptional
		}
		result[index] = ClassifiedStop{
			Order:          index,
			ID:             stop.ID,
			DistanceMeters: distance,
			Availability:   availability,
		}
	}
	return result
}

func distanceToLineString(point route.Position, line route.LineString) float64 {
	minimum := math.Inf(1)
	for index := 0; index < len(line.Positions)-1; index++ {
		distance := distanceToSegment(
			point,
			line.Positions[index],
			line.Positions[index+1],
		)
		minimum = math.Min(minimum, distance)
	}
	return minimum
}

// distanceToSegment measures the shortest spherical distance to the minor
// great-circle arc from start to end. Cuts are limited to 20 km, so the arc is
// unambiguous and never approaches the antipodal singularity.
func distanceToSegment(point, start, end route.Position) float64 {
	segmentAngular := route.DistanceMeters(start, end) / earthRadiusMeters
	startToPointAngular := route.DistanceMeters(start, point) / earthRadiusMeters
	if segmentAngular == 0 || startToPointAngular == 0 {
		return startToPointAngular * earthRadiusMeters
	}

	startBearing := initialBearing(start, end)
	pointBearing := initialBearing(start, point)
	bearingDelta := pointBearing - startBearing
	crossTrack := math.Asin(clamp(
		math.Sin(startToPointAngular)*math.Sin(bearingDelta),
		-1,
		1,
	))
	alongTrack := math.Atan2(
		math.Sin(startToPointAngular)*math.Cos(bearingDelta),
		math.Cos(startToPointAngular),
	)
	if alongTrack >= 0 && alongTrack <= segmentAngular {
		return math.Abs(crossTrack) * earthRadiusMeters
	}
	return math.Min(
		route.DistanceMeters(point, start),
		route.DistanceMeters(point, end),
	)
}

func initialBearing(from, to route.Position) float64 {
	fromLatitude := radians(from.Latitude)
	toLatitude := radians(to.Latitude)
	longitudeDelta := radians(to.Longitude - from.Longitude)
	return math.Atan2(
		math.Sin(longitudeDelta)*math.Cos(toLatitude),
		math.Cos(fromLatitude)*math.Sin(toLatitude)-
			math.Sin(fromLatitude)*math.Cos(toLatitude)*math.Cos(longitudeDelta),
	)
}

func radians(degrees float64) float64 { return degrees * math.Pi / 180 }

func clamp(value, minimum, maximum float64) float64 {
	return math.Min(maximum, math.Max(minimum, value))
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
