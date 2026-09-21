package detour

import (
	"fmt"
	"math"
	"sort"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

type routeReplacement struct {
	start routeLocation
	end   routeLocation
	path  route.LineString
}

func reconstructRoute(
	original route.Route,
	omitted map[int]struct{},
	replacements []routeReplacement,
) (route.Route, error) {
	if len(original.Stops)-len(omitted) < 2 {
		return route.Route{}, &Error{
			Code:    ErrorNoDetourWithinSearchArea,
			Kind:    ErrorKindNoSolution,
			Message: "the cut leaves fewer than two covered stops",
		}
	}
	sort.Slice(replacements, func(left, right int) bool {
		if replacements[left].start.scalar() != replacements[right].start.scalar() {
			return replacements[left].start.scalar() < replacements[right].start.scalar()
		}
		return replacements[left].end.scalar() < replacements[right].end.scalar()
	})
	for index, replacement := range replacements {
		if replacement.start.scalar() >= replacement.end.scalar()-locationTolerance ||
			len(replacement.path.Positions) < 2 {
			return route.Route{}, fmt.Errorf("detour replacement %d is empty or reversed", index)
		}
		if index > 0 && replacements[index-1].end.scalar() >
			replacement.start.scalar()+locationTolerance {
			return route.Route{}, fmt.Errorf("detour replacements %d and %d overlap", index-1, index)
		}
	}

	keptOrders := make([]int, 0, len(original.Stops)-len(omitted))
	for order := range original.Stops {
		if _, removed := omitted[order]; !removed {
			keptOrders = append(keptOrders, order)
		}
	}
	variant := route.Route{
		Jurisdiction: original.Jurisdiction,
		Stops:        make([]route.Stop, len(keptOrders)),
	}
	for index, originalOrder := range keptOrders {
		variant.Stops[index] = route.Stop{
			ID:       original.Stops[originalOrder].ID,
			Position: original.Stops[originalOrder].Position,
		}
		if index == len(keptOrders)-1 {
			continue
		}
		start := stopLocation(originalOrder, len(original.Stops))
		end := stopLocation(keptOrders[index+1], len(original.Stops))
		path, err := reconstructPath(original, start, end, replacements)
		if err != nil {
			return route.Route{}, fmt.Errorf(
				"reconstruct path from stop %d to %d: %w",
				originalOrder,
				keptOrders[index+1],
				err,
			)
		}
		variant.Stops[index].PathToNext = &path
	}
	return variant, nil
}

func reconstructPath(
	original route.Route,
	start, end routeLocation,
	replacements []routeReplacement,
) (route.LineString, error) {
	cursor := start
	var positions []route.Position
	for _, replacement := range replacements {
		if replacement.end.scalar() <= start.scalar()+locationTolerance ||
			replacement.start.scalar() >= end.scalar()-locationTolerance {
			continue
		}
		if replacement.start.scalar() < cursor.scalar()-locationTolerance ||
			replacement.end.scalar() > end.scalar()+locationTolerance {
			return route.LineString{}, fmt.Errorf("replacement crosses retained stop boundary")
		}
		originalPiece, err := originalSlice(original, cursor, replacement.start)
		if err != nil {
			return route.LineString{}, err
		}
		positions = appendPositions(positions, originalPiece.Positions)
		positions = appendPositions(positions, replacement.path.Positions)
		cursor = replacement.end
	}
	originalPiece, err := originalSlice(original, cursor, end)
	if err != nil {
		return route.LineString{}, err
	}
	positions = appendPositions(positions, originalPiece.Positions)
	if len(positions) < 2 {
		return route.LineString{}, fmt.Errorf("reconstructed path has fewer than two positions")
	}
	return route.LineString{Positions: positions}, nil
}

func originalSlice(
	original route.Route,
	start, end routeLocation,
) (route.LineString, error) {
	startScalar := start.scalar()
	endScalar := end.scalar()
	if startScalar > endScalar+locationTolerance || startScalar < 0 ||
		endScalar > float64(len(original.Stops)-1)+locationTolerance {
		return route.LineString{}, fmt.Errorf("invalid original route slice %.9f to %.9f", startScalar, endScalar)
	}
	if endScalar-startScalar <= locationTolerance {
		return route.LineString{}, nil
	}

	firstSegment := int(math.Floor(startScalar))
	lastSegment := int(math.Ceil(endScalar)) - 1
	var positions []route.Position
	for segmentOrder := firstSegment; segmentOrder <= lastSegment; segmentOrder++ {
		if segmentOrder < 0 || segmentOrder >= len(original.Stops)-1 ||
			original.Stops[segmentOrder].PathToNext == nil {
			return route.LineString{}, fmt.Errorf("original segment %d is unavailable", segmentOrder)
		}
		fromFraction := math.Max(0, startScalar-float64(segmentOrder))
		toFraction := math.Min(1, endScalar-float64(segmentOrder))
		if toFraction-fromFraction <= locationTolerance {
			continue
		}
		piece, err := lineSubstring(*original.Stops[segmentOrder].PathToNext, fromFraction, toFraction)
		if err != nil {
			return route.LineString{}, fmt.Errorf("slice original segment %d: %w", segmentOrder, err)
		}
		positions = appendPositions(positions, piece.Positions)
	}
	return route.LineString{Positions: positions}, nil
}

// lineSubstring mirrors PostGIS' fraction convention for a WGS 84 geometry:
// fractions use the planar length of each coordinate segment.
func lineSubstring(line route.LineString, fromFraction, toFraction float64) (route.LineString, error) {
	if len(line.Positions) < 2 || fromFraction < 0 || toFraction > 1 ||
		fromFraction > toFraction {
		return route.LineString{}, fmt.Errorf("invalid LineString substring")
	}
	lengths := make([]float64, len(line.Positions)-1)
	var total float64
	for index := range lengths {
		left := line.Positions[index]
		right := line.Positions[index+1]
		lengths[index] = math.Hypot(
			right.Longitude-left.Longitude,
			right.Latitude-left.Latitude,
		)
		total += lengths[index]
	}
	if total == 0 {
		return route.LineString{}, fmt.Errorf("LineString has zero planar length")
	}
	startDistance := fromFraction * total
	endDistance := toFraction * total
	start := interpolateAtDistance(line.Positions, lengths, startDistance)
	end := interpolateAtDistance(line.Positions, lengths, endDistance)
	positions := []route.Position{start}
	var traversed float64
	for index, length := range lengths {
		traversed += length
		if traversed > startDistance && traversed < endDistance {
			positions = append(positions, line.Positions[index+1])
		}
	}
	positions = appendPositions(positions, []route.Position{end})
	return route.LineString{Positions: positions}, nil
}

func interpolateAtDistance(
	positions []route.Position,
	lengths []float64,
	target float64,
) route.Position {
	if target <= 0 {
		return positions[0]
	}
	var traversed float64
	for index, length := range lengths {
		if target <= traversed+length || index == len(lengths)-1 {
			if length == 0 {
				return positions[index+1]
			}
			fraction := (target - traversed) / length
			left := positions[index]
			right := positions[index+1]
			return route.Position{
				Latitude:  left.Latitude + fraction*(right.Latitude-left.Latitude),
				Longitude: left.Longitude + fraction*(right.Longitude-left.Longitude),
			}
		}
		traversed += length
	}
	return positions[len(positions)-1]
}

func appendPositions(destination, source []route.Position) []route.Position {
	if len(source) == 0 {
		return destination
	}
	start := 0
	if len(destination) > 0 && samePosition(destination[len(destination)-1], source[0]) {
		start = 1
	}
	return append(destination, source[start:]...)
}

func samePosition(left, right route.Position) bool {
	return math.Abs(left.Latitude-right.Latitude) <= 1e-10 &&
		math.Abs(left.Longitude-right.Longitude) <= 1e-10
}

func stopDirection(input route.Route, order int) float64 {
	stop := input.Stops[order].Position
	before, hasBefore := neighboringPosition(input, order, false)
	after, hasAfter := neighboringPosition(input, order, true)
	switch {
	case hasBefore && hasAfter:
		return bearingDegrees(before, after)
	case hasAfter:
		return bearingDegrees(stop, after)
	case hasBefore:
		return bearingDegrees(before, stop)
	default:
		return 0
	}
}

func neighboringPosition(input route.Route, order int, forward bool) (route.Position, bool) {
	const minimumSeparationMeters = 0.1
	stop := input.Stops[order].Position
	if forward && order < len(input.Stops)-1 && input.Stops[order].PathToNext != nil {
		for _, position := range input.Stops[order].PathToNext.Positions {
			if route.DistanceMeters(stop, position) > minimumSeparationMeters {
				return position, true
			}
		}
	}
	if !forward && order > 0 && input.Stops[order-1].PathToNext != nil {
		positions := input.Stops[order-1].PathToNext.Positions
		for index := len(positions) - 1; index >= 0; index-- {
			if route.DistanceMeters(stop, positions[index]) > minimumSeparationMeters {
				return positions[index], true
			}
		}
	}
	return route.Position{}, false
}

func bearingDegrees(from, to route.Position) float64 {
	fromLatitude := from.Latitude * math.Pi / 180
	toLatitude := to.Latitude * math.Pi / 180
	longitudeDelta := (to.Longitude - from.Longitude) * math.Pi / 180
	bearing := math.Atan2(
		math.Sin(longitudeDelta)*math.Cos(toLatitude),
		math.Cos(fromLatitude)*math.Sin(toLatitude)-
			math.Sin(fromLatitude)*math.Cos(toLatitude)*math.Cos(longitudeDelta),
	) * 180 / math.Pi
	if bearing < 0 {
		bearing += 360
	}
	return bearing
}
