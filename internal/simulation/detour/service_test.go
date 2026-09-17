package detour

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

type fakeRepository struct {
	intersects      bool
	intersectsError error
	connections     []Connection
	connectionsCall int
	routingInput    RoutingInput
}

func (repository *fakeRepository) CutIntersectsGraph(
	context.Context,
	Cut,
) (bool, error) {
	return repository.intersects, repository.intersectsError
}

func (repository *fakeRepository) FindConnections(
	_ context.Context,
	input RoutingInput,
) ([]Connection, error) {
	repository.connectionsCall++
	repository.routingInput = input
	return repository.connections, nil
}

func TestServicePlanValidatesClassifiesAndSelects(t *testing.T) {
	repository := &fakeRepository{
		intersects: true,
		connections: []Connection{
			connection(0, 1, 5, 1),
			connection(1, 2, 5, 2),
			connection(0, 2, 9, 3),
		},
	}
	service := NewService(repository, testPolicy())
	input := Input{
		Route:     validRoute(),
		Cut:       cutAt(position(0.0009, 0.009), position(0.0009, 0.011)),
		Criterion: CriterionShortestTime,
	}

	got, err := service.Plan(context.Background(), input)
	if err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}
	if repository.connectionsCall != 1 {
		t.Fatalf("FindConnections calls = %d, want 1", repository.connectionsCall)
	}
	wantAvailability := []StopAvailability{StopRequired, StopOptional, StopRequired}
	for index, stop := range got.Stops {
		if stop.Availability != wantAvailability[index] {
			t.Fatalf("stop %d availability = %q, want %q", stop.Order, stop.Availability, wantAvailability[index])
		}
	}
	if !reflect.DeepEqual(got.Selection.KeptStopOrders, []int{0, 2}) {
		t.Fatalf("kept stops = %v, want [0 2]", got.Selection.KeptStopOrders)
	}
	if !reflect.DeepEqual(repository.routingInput.Stops, got.Stops) {
		t.Fatal("repository did not receive the classified stops")
	}
}

func TestServicePlanRejectsCutOutsideGraphBeforeRouting(t *testing.T) {
	repository := &fakeRepository{intersects: false}
	service := NewService(repository, testPolicy())

	_, err := service.Plan(context.Background(), Input{
		Route:     validRoute(),
		Cut:       cutAt(position(0, 0), position(0, 0.001)),
		Criterion: CriterionShortestTime,
	})
	var detourError *Error
	if !errors.As(err, &detourError) ||
		detourError.Code != ErrorCutOutsideGraph ||
		detourError.Kind != ErrorKindInvalidInput {
		t.Fatalf("Plan error = %#v, want cut_outside_graph", err)
	}
	if repository.connectionsCall != 0 {
		t.Fatalf("FindConnections calls = %d, want 0", repository.connectionsCall)
	}
}

func TestServicePlanRejectsInvalidCutBeforeRepository(t *testing.T) {
	repository := &fakeRepository{intersects: true}
	service := NewService(repository, testPolicy())

	_, err := service.Plan(context.Background(), Input{
		Route:     validRoute(),
		Cut:       cutAt(position(0, 0), position(0, 0)),
		Criterion: CriterionShortestTime,
	})
	var detourError *Error
	if !errors.As(err, &detourError) || detourError.Code != ErrorInvalidCut {
		t.Fatalf("Plan error = %#v, want invalid_cut", err)
	}
	if repository.connectionsCall != 0 {
		t.Fatalf("FindConnections calls = %d, want 0", repository.connectionsCall)
	}
}

func validRoute() route.Route {
	firstPath := route.LineString{Positions: []route.Position{
		position(0, 0),
		position(0, 0.01),
	}}
	secondPath := route.LineString{Positions: []route.Position{
		position(0, 0.01),
		position(0, 0.02),
	}}
	return route.Route{
		Jurisdiction: route.JurisdictionCABA,
		Stops: []route.Stop{
			{ID: "A", Position: position(0, 0), PathToNext: &firstPath},
			{ID: "B", Position: position(0, 0.01), PathToNext: &secondPath},
			{ID: "C", Position: position(0, 0.02)},
		},
	}
}
