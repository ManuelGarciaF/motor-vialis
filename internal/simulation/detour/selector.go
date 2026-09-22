package detour

import (
	"fmt"
	"math"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// pgrouting can split the same physical path at a virtual stop, changing the
// order of floating-point additions. Differences below one nanosecond are
// numerical noise, not a meaningful traffic-time advantage.
const travelTimeTieToleranceSeconds = 1e-9

type selectionState struct {
	kept        []int
	connections []Connection
	seconds     float64
}

// Select resolves the ordered stop DAG according to the requested
// lexicographic criterion. Required stops can never be skipped and forced stops
// can never be retained.
func Select(
	stops []ClassifiedStop,
	connections []Connection,
	criterion Criterion,
) (Selection, error) {
	if err := validateCriterion(criterion); err != nil {
		return Selection{}, err
	}
	if err := validateSelectorInput(stops, connections); err != nil {
		return Selection{}, err
	}

	incoming := make(map[int][]Connection)
	for _, connection := range connections {
		incoming[connection.ToStopOrder] = append(
			incoming[connection.ToStopOrder],
			cloneConnection(connection),
		)
	}

	// Keep separate states for paths with one retained stop and paths with at
	// least two. Without that dimension, a zero-cost one-stop prefix could
	// discard the only state that is itself a valid route.
	bestByEnd := make([][2]*selectionState, len(stops))
	for end := range stops {
		if stops[end].Availability == StopForcedUnavailable {
			continue
		}
		if !containsRequired(stops[:end]) {
			bestByEnd[end][0] = &selectionState{kept: []int{end}}
		}

		for _, connection := range incoming[end] {
			start := connection.FromStopOrder
			if containsRequired(stops[start+1 : end]) {
				continue
			}
			for category, previous := range bestByEnd[start] {
				if previous == nil {
					continue
				}
				candidate := &selectionState{
					kept:        appendCopy(previous.kept, end),
					connections: appendConnection(previous.connections, connection),
					seconds:     previous.seconds + connection.TravelSeconds,
				}
				candidateCategory := min(1, category+1)
				current := bestByEnd[end][candidateCategory]
				if current == nil || statePrecedes(
					candidate,
					current,
					criterion,
					end+1,
				) {
					bestByEnd[end][candidateCategory] = candidate
				}
			}
		}
	}

	var best *selectionState
	for end, states := range bestByEnd {
		candidate := states[1]
		if candidate == nil || containsRequired(stops[end+1:]) {
			continue
		}
		if best == nil || statePrecedes(
			candidate,
			best,
			criterion,
			len(stops),
		) {
			best = candidate
		}
	}
	if best == nil {
		return Selection{}, &Error{
			Code:    ErrorNoDetourWithinSearchArea,
			Kind:    ErrorKindNoSolution,
			Message: "no path retains every required stop and at least two stops total",
		}
	}

	keptSet := make(map[int]struct{}, len(best.kept))
	for _, order := range best.kept {
		keptSet[order] = struct{}{}
	}
	omitted := make([]int, 0, len(stops)-len(best.kept))
	for order := range stops {
		if _, retained := keptSet[order]; !retained {
			omitted = append(omitted, order)
		}
	}
	return Selection{
		KeptStopOrders:    append([]int(nil), best.kept...),
		OmittedStopOrders: omitted,
		TravelSeconds:     best.seconds,
		Connections:       cloneConnections(best.connections),
	}, nil
}

func validateCriterion(criterion Criterion) error {
	if criterion != CriterionShortestTime && criterion != CriterionFewestLostStops {
		return &ValidationError{
			Field:   "criterion",
			Message: "must be MENOR_TIEMPO or MENOR_PARADAS_PERDIDAS",
		}
	}
	return nil
}

func validateSelectorInput(stops []ClassifiedStop, connections []Connection) error {
	for index, stop := range stops {
		if stop.Order != index {
			return fmt.Errorf("select detour: stop order %d at position %d", stop.Order, index)
		}
		switch stop.Availability {
		case StopForcedUnavailable, StopOptional, StopRequired:
		default:
			return fmt.Errorf("select detour: stop %d has invalid availability %q", index, stop.Availability)
		}
	}
	for _, connection := range connections {
		if connection.FromStopOrder < 0 ||
			connection.ToStopOrder >= len(stops) ||
			connection.FromStopOrder >= connection.ToStopOrder {
			return fmt.Errorf(
				"select detour: invalid connection %d -> %d",
				connection.FromStopOrder,
				connection.ToStopOrder,
			)
		}
		if connection.TravelSeconds < 0 ||
			math.IsNaN(connection.TravelSeconds) ||
			math.IsInf(connection.TravelSeconds, 0) {
			return fmt.Errorf(
				"select detour: connection %d -> %d has invalid travel time",
				connection.FromStopOrder,
				connection.ToStopOrder,
			)
		}
	}
	return nil
}

func containsRequired(stops []ClassifiedStop) bool {
	for _, stop := range stops {
		if stop.Availability == StopRequired {
			return true
		}
	}
	return false
}

func statePrecedes(
	candidate, current *selectionState,
	criterion Criterion,
	consideredStops int,
) bool {
	candidateOmitted := consideredStops - len(candidate.kept)
	currentOmitted := consideredStops - len(current.kept)
	secondsDiffer := math.Abs(candidate.seconds-current.seconds) >
		travelTimeTieToleranceSeconds
	if criterion == CriterionShortestTime {
		if secondsDiffer {
			return candidate.seconds < current.seconds
		}
		if candidateOmitted != currentOmitted {
			return candidateOmitted < currentOmitted
		}
	} else {
		if candidateOmitted != currentOmitted {
			return candidateOmitted < currentOmitted
		}
		if secondsDiffer {
			return candidate.seconds < current.seconds
		}
	}
	if comparison := compareInts(candidate.kept, current.kept); comparison != 0 {
		return comparison < 0
	}
	return compareInts64(
		flattenEdgeIDs(candidate.connections),
		flattenEdgeIDs(current.connections),
	) < 0
}

func compareInts(left, right []int) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func compareInts64(left, right []int64) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func flattenEdgeIDs(connections []Connection) []int64 {
	var result []int64
	for _, connection := range connections {
		result = append(result, connection.EdgeIDs...)
	}
	return result
}

func appendCopy(values []int, value int) []int {
	result := make([]int, len(values), len(values)+1)
	copy(result, values)
	return append(result, value)
}

func appendConnection(values []Connection, value Connection) []Connection {
	result := cloneConnections(values)
	return append(result, cloneConnection(value))
}

func cloneConnections(values []Connection) []Connection {
	result := make([]Connection, len(values))
	for index, value := range values {
		result[index] = cloneConnection(value)
	}
	return result
}

func cloneConnection(value Connection) Connection {
	value.EdgeIDs = append([]int64(nil), value.EdgeIDs...)
	value.Path.Positions = append([]route.Position(nil), value.Path.Positions...)
	return value
}
