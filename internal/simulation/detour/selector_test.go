package detour

import (
	"errors"
	"reflect"
	"testing"
)

func TestSelectCriteriaDivergeWithoutImplicitStopPenalty(t *testing.T) {
	stops := classified(
		StopRequired,
		StopOptional,
		StopOptional,
		StopRequired,
	)
	connections := []Connection{
		connection(0, 1, 5, 1),
		connection(1, 2, 5, 2),
		connection(2, 3, 5, 3),
		connection(0, 3, 14.9, 4),
	}

	shortest, err := Select(stops, connections, CriterionShortestTime)
	if err != nil {
		t.Fatalf("Select shortest returned error: %v", err)
	}
	if !reflect.DeepEqual(shortest.KeptStopOrders, []int{0, 3}) {
		t.Fatalf("shortest kept = %v, want [0 3]", shortest.KeptStopOrders)
	}

	fewestLost, err := Select(stops, connections, CriterionFewestLostStops)
	if err != nil {
		t.Fatalf("Select fewest lost returned error: %v", err)
	}
	if !reflect.DeepEqual(fewestLost.KeptStopOrders, []int{0, 1, 2, 3}) {
		t.Fatalf("fewest lost kept = %v, want [0 1 2 3]", fewestLost.KeptStopOrders)
	}
}

func TestSelectNeverRetainsForcedOrSkipsRequiredStops(t *testing.T) {
	stops := classified(
		StopRequired,
		StopForcedUnavailable,
		StopRequired,
		StopOptional,
	)
	connections := []Connection{
		connection(0, 2, 10, 1),
		connection(0, 3, 1, 2), // Invalid alternative: it skips required stop 2.
		connection(2, 3, 10, 3),
	}

	got, err := Select(stops, connections, CriterionShortestTime)
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if !reflect.DeepEqual(got.KeptStopOrders, []int{0, 2}) {
		t.Fatalf("kept = %v, want [0 2]", got.KeptStopOrders)
	}
	if !reflect.DeepEqual(got.OmittedStopOrders, []int{1, 3}) {
		t.Fatalf("omitted = %v, want [1 3]", got.OmittedStopOrders)
	}
}

func TestSelectAllowsOptionalTerminalStopsToBeOmitted(t *testing.T) {
	stops := classified(StopOptional, StopRequired, StopRequired, StopOptional)
	connections := []Connection{
		connection(0, 1, 100, 1),
		connection(1, 2, 10, 2),
		connection(2, 3, 100, 3),
	}

	got, err := Select(stops, connections, CriterionShortestTime)
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if !reflect.DeepEqual(got.KeptStopOrders, []int{1, 2}) {
		t.Fatalf("kept = %v, want [1 2]", got.KeptStopOrders)
	}
}

func TestSelectUsesStableEdgeIDTieBreak(t *testing.T) {
	stops := classified(StopRequired, StopRequired)
	connections := []Connection{
		connection(0, 1, 10, 9),
		connection(0, 1, 10, 3),
	}

	got, err := Select(stops, connections, CriterionShortestTime)
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if got.Connections[0].EdgeIDs[0] != 3 {
		t.Fatalf("selected edge ID = %d, want 3", got.Connections[0].EdgeIDs[0])
	}
}

func TestSelectReturnsTypedErrorWhenNoVariantExists(t *testing.T) {
	_, err := Select(
		classified(StopRequired, StopRequired),
		nil,
		CriterionShortestTime,
	)
	var detourError *Error
	if !errors.As(err, &detourError) ||
		detourError.Code != ErrorNoDetourWithinSearchArea ||
		detourError.Kind != ErrorKindNoSolution {
		t.Fatalf("Select error = %#v, want no_detour_within_search_area", err)
	}
}

func classified(values ...StopAvailability) []ClassifiedStop {
	result := make([]ClassifiedStop, len(values))
	for index, value := range values {
		result[index] = ClassifiedStop{Order: index, ID: string(rune('A' + index)), Availability: value}
	}
	return result
}

func connection(from, to int, seconds float64, edgeIDs ...int64) Connection {
	return Connection{
		FromStopOrder: from,
		ToStopOrder:   to,
		TravelSeconds: seconds,
		EdgeIDs:       edgeIDs,
	}
}
