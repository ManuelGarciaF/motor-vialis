package simulation

import (
	"fmt"
	"math"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// newDetourStop marks a proposed stop whose ID does not exist in the baseline.
const newDetourStop = -1

// validateDetourStops requires every proposed stop that also exists in the
// baseline to keep the baseline order. Stops absent from the baseline are new
// and may appear anywhere, including first or last.
func validateDetourStops(input ComparisonInput) error {
	baselineOrder := baselineStopOrder(input.Baseline)

	previous := -1
	for index, stop := range input.Proposed.Stops {
		order, found := baselineOrder[stop.ID]
		if !found {
			continue
		}
		if order <= previous {
			return &route.ValidationError{
				Field: fmt.Sprintf("proposed.stops[%d].id", index),
				Message: "must keep the baseline stop order: a detour may skip " +
					"baseline stops or add new ones, but not reorder retained ones",
			}
		}

		previous = order
	}

	return nil
}

// detourFareTravelTime charges each proposed stop pair by the distance between
// both stops along the baseline route, so passengers pay as if the bus had
// taken the original route.
//
// A retained stop sits at the cumulative measured baseline distance up to it.
// A new stop is projected onto the baseline geometry (each segment's
// PathToNext, the exact street path the baseline was measured on) and placed at
// the measured distance of the segments before it plus the projected fraction
// of its own segment's measured distance. The projection only considers the
// baseline stretch between the retained stops around the new stop in the
// proposed sequence (route start and end when there are none), so a stop near
// the return leg cannot snap onto it. Positions never decrease along the
// proposed sequence.
func detourFareTravelTime(
	input ComparisonInput,
	baselineSegments []traveltime.SegmentResult,
) (traveltime.Result, error) {
	baselineStops := input.Baseline.Stops
	if len(baselineSegments) != len(baselineStops)-1 {
		return traveltime.Result{}, fmt.Errorf("baseline travel time has %d segments, want %d", len(baselineSegments), len(baselineStops)-1)
	}
	for index, segment := range baselineSegments {
		if segment.OriginStopID != baselineStops[index].ID ||
			segment.DestinationStopID != baselineStops[index+1].ID {
			return traveltime.Result{}, fmt.Errorf("baseline travel-time segments do not match baseline route")
		}
	}

	orders := proposedBaselineOrders(input)
	positions, err := detourFarePositions(input, orders, baselineSegments)
	if err != nil {
		return traveltime.Result{}, err
	}

	proposedStops := input.Proposed.Stops
	result := traveltime.Result{BySegment: make([]traveltime.SegmentResult, 0, len(proposedStops)-1)}
	for order := 0; order < len(proposedStops)-1; order++ {
		var distance float64
		from, to := orders[order], orders[order+1]
		if from != newDetourStop && to != newDetourStop {
			// Summing segment by segment keeps retained pairs bit-identical to
			// the original subset-only calculation.
			for index := from; index < to; index++ {
				distance += baselineSegments[index].DistanceMeters
			}
		} else {
			distance = positions[order+1] - positions[order]
		}

		result.BySegment = append(result.BySegment, traveltime.SegmentResult{
			OriginStopID:      proposedStops[order].ID,
			DestinationStopID: proposedStops[order+1].ID,
			DistanceMeters:    distance,
		})
	}

	return result, nil
}

func baselineStopOrder(baseline route.Route) map[string]int {
	orders := make(map[string]int, len(baseline.Stops))
	for order, stop := range baseline.Stops {
		orders[stop.ID] = order
	}
	return orders
}

// proposedBaselineOrders returns each proposed stop's baseline index, or
// newDetourStop when the stop does not exist in the baseline.
func proposedBaselineOrders(input ComparisonInput) []int {
	baselineOrder := baselineStopOrder(input.Baseline)
	orders := make([]int, len(input.Proposed.Stops))
	for index, stop := range input.Proposed.Stops {
		order, found := baselineOrder[stop.ID]
		if !found {
			order = newDetourStop
		}
		orders[index] = order
	}
	return orders
}

// detourFarePositions returns each proposed stop's distance from the baseline
// start, measured along the baseline route.
func detourFarePositions(
	input ComparisonInput,
	orders []int,
	baselineSegments []traveltime.SegmentResult,
) ([]float64, error) {
	cumulative := make([]float64, len(input.Baseline.Stops))
	for index, segment := range baselineSegments {
		cumulative[index+1] = cumulative[index] + segment.DistanceMeters
	}

	positions := make([]float64, len(orders))
	lower := 0
	for index, order := range orders {
		if order != newDetourStop {
			positions[index] = cumulative[order]
			lower = order
			continue
		}

		upper := len(input.Baseline.Stops) - 1
		for _, next := range orders[index+1:] {
			if next != newDetourStop {
				upper = next
				break
			}
		}

		position, err := projectOntoBaseline(
			input.Baseline,
			baselineSegments,
			cumulative,
			lower,
			upper,
			input.Proposed.Stops[index].Position,
		)
		if err != nil {
			return nil, err
		}
		if index > 0 && position < positions[index-1] {
			position = positions[index-1]
		}
		positions[index] = position
	}

	return positions, nil
}

// projectOntoBaseline finds the point of the baseline path between stops lower
// and upper closest to target and returns its distance from the route start.
func projectOntoBaseline(
	baseline route.Route,
	baselineSegments []traveltime.SegmentResult,
	cumulative []float64,
	lower int,
	upper int,
	target route.Position,
) (float64, error) {
	if lower >= upper {
		return cumulative[lower], nil
	}

	best := math.Inf(1)
	var position float64
	for segment := lower; segment < upper; segment++ {
		path := baseline.Stops[segment].PathToNext
		if path == nil || len(path.Positions) < 2 {
			return 0, fmt.Errorf("baseline stop %d has no path to project new stops onto", segment)
		}

		distance, fraction := closestFraction(path.Positions, target)
		if distance < best {
			best = distance
			position = cumulative[segment] + fraction*baselineSegments[segment].DistanceMeters
		}
	}

	return position, nil
}

// closestFraction projects target onto a polyline and returns the planar
// distance to it (in scaled degrees, only meaningful for comparison) and the
// fraction of the polyline's geodesic length before the projected point.
func closestFraction(path []route.Position, target route.Position) (float64, float64) {
	// An equirectangular plane centred on the target is accurate enough at
	// street scale to choose the nearest point.
	scale := math.Cos(target.Latitude * math.Pi / 180)
	planar := func(position route.Position) (float64, float64) {
		return (position.Longitude - target.Longitude) * scale, position.Latitude - target.Latitude
	}

	best := math.Inf(1)
	var bestAlong, total float64
	for index := 0; index < len(path)-1; index++ {
		startX, startY := planar(path[index])
		endX, endY := planar(path[index+1])
		deltaX, deltaY := endX-startX, endY-startY
		length := route.DistanceMeters(path[index], path[index+1])

		var ratio float64
		if squared := deltaX*deltaX + deltaY*deltaY; squared > 0 {
			ratio = math.Max(0, math.Min(1, -(startX*deltaX+startY*deltaY)/squared))
		}
		distance := math.Hypot(startX+ratio*deltaX, startY+ratio*deltaY)
		if distance < best {
			best = distance
			bestAlong = total + ratio*length
		}
		total += length
	}

	if total == 0 {
		return best, 0
	}
	return best, bestAlong / total
}
