package simulation

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestServiceSimulateAssignsCellsAndBuildsTotals(t *testing.T) {
	repository := &fakeRepository{
		candidates: []CellCandidate{
			{StopOrder: 2, StopID: "C", CellID: "cell-c", DistanceMeters: 80},
			{StopOrder: 1, StopID: "B", CellID: "shared", DistanceMeters: 160},
			{StopOrder: 0, StopID: "A", CellID: "cell-a", DistanceMeters: 40},
			{StopOrder: 0, StopID: "A", CellID: "shared", DistanceMeters: 240},
		},
		pairDemand: []StopPairDemand{
			{OriginStopOrder: 0, OriginStopID: "A", DestinationStopOrder: 1, DestinationStopID: "B", GrossDemand: 100, PotentialDemand: 70},
			{OriginStopOrder: 0, OriginStopID: "A", DestinationStopOrder: 2, DestinationStopID: "C", GrossDemand: 50, PotentialDemand: 30},
			{OriginStopOrder: 1, OriginStopID: "B", DestinationStopOrder: 2, DestinationStopID: "C", GrossDemand: 25, PotentialDemand: 18},
		},
	}
	service := NewService(repository, 800, LinearAccessibility{})
	route := routeWithStops("A", "B", "C")

	result, err := service.Simulate(context.Background(), route)
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}

	if repository.receivedRadius != 800 {
		t.Fatalf("radius = %v, want 800", repository.receivedRadius)
	}
	if !reflect.DeepEqual(repository.receivedRoute, route) {
		t.Fatalf("route = %#v, want %#v", repository.receivedRoute, route)
	}
	wantCells := []AssignedCell{
		{StopOrder: 0, StopID: "A", CellID: "cell-a", Accessibility: 0.95},
		{StopOrder: 1, StopID: "B", CellID: "shared", Accessibility: 0.8},
		{StopOrder: 2, StopID: "C", CellID: "cell-c", Accessibility: 0.9},
	}
	if !reflect.DeepEqual(repository.receivedCells, wantCells) {
		t.Fatalf("assigned cells = %#v, want %#v", repository.receivedCells, wantCells)
	}
	assertFloat(t, "GrossDemand", result.GrossDemand, 175)
	assertFloat(t, "PotentialDemand", result.PotentialDemand, 118)
	if !reflect.DeepEqual(result.ByStopPair, repository.pairDemand) {
		t.Fatalf("ByStopPair = %#v, want %#v", result.ByStopPair, repository.pairDemand)
	}
}

func TestAssignCellsUsesDeterministicPriority(t *testing.T) {
	candidates := []CellCandidate{
		{StopOrder: 1, StopID: "B", CellID: "tie-order", DistanceMeters: 400},
		{StopOrder: 0, StopID: "A", CellID: "tie-order", DistanceMeters: 400},
		{StopOrder: 1, StopID: "B", CellID: "nearest", DistanceMeters: 200},
		{StopOrder: 0, StopID: "A", CellID: "nearest", DistanceMeters: 400},
		{StopOrder: 2, StopID: "C", CellID: "only-c", DistanceMeters: 0},
		{StopOrder: 1, StopID: "Z", CellID: "tie-id", DistanceMeters: 100},
		{StopOrder: 1, StopID: "B", CellID: "tie-id", DistanceMeters: 100},
	}

	actual := assignCells(candidates, 800, LinearAccessibility{})
	want := []AssignedCell{
		{StopOrder: 0, StopID: "A", CellID: "tie-order", Accessibility: 0.5},
		{StopOrder: 1, StopID: "B", CellID: "nearest", Accessibility: 0.75},
		{StopOrder: 1, StopID: "B", CellID: "tie-id", Accessibility: 0.875},
		{StopOrder: 2, StopID: "C", CellID: "only-c", Accessibility: 1},
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("assignCells() = %#v, want %#v", actual, want)
	}
}

func TestServiceUsesAccessibilityCalculator(t *testing.T) {
	repository := &fakeRepository{
		candidates: []CellCandidate{
			{StopOrder: 0, StopID: "A", CellID: "cell-a", DistanceMeters: 400},
			{StopOrder: 1, StopID: "B", CellID: "cell-b", DistanceMeters: 0},
		},
	}
	service := NewService(repository, 800, QuadraticAccessibility{})

	if _, err := service.Simulate(
		context.Background(),
		routeWithStops("A", "B"),
	); err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}

	want := []AssignedCell{
		{StopOrder: 0, StopID: "A", CellID: "cell-a", Accessibility: 0.25},
		{StopOrder: 1, StopID: "B", CellID: "cell-b", Accessibility: 1},
	}
	if !reflect.DeepEqual(repository.receivedCells, want) {
		t.Fatalf(
			"assigned cells = %#v, want %#v",
			repository.receivedCells,
			want,
		)
	}
}

func TestServiceSimulateRejectsInvalidRoute(t *testing.T) {
	tests := []struct {
		name  string
		route Route
		field string
	}{
		{name: "not enough stops", route: routeWithStops("A"), field: "route.stops"},
		{name: "empty ID", route: routeWithStops("A", " "), field: "route.stops[1].id"},
		{name: "duplicate ID", route: routeWithStops("A", "A"), field: "route.stops[1].id"},
		{
			name: "invalid latitude",
			route: Route{Stops: []Stop{
				{ID: "A", Position: Position{Latitude: 91}},
				{ID: "B"},
			}},
			field: "route.stops[0].position.latitude",
		},
		{
			name: "invalid longitude",
			route: Route{Stops: []Stop{
				{ID: "A", Position: Position{Longitude: -181}},
				{ID: "B"},
			}},
			field: "route.stops[0].position.longitude",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{}
			_, err := NewService(
				repository,
				800,
				LinearAccessibility{},
			).Simulate(context.Background(), test.route)
			if err == nil {
				t.Fatal("Simulate() error = nil, want validation error")
			}
			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error type = %T, want *ValidationError", err)
			}
			if validationError.Field != test.field {
				t.Fatalf("field = %q, want %q", validationError.Field, test.field)
			}
			if repository.findCandidatesCalls != 0 {
				t.Fatal("repository was called for an invalid route")
			}
		})
	}
}

func TestServiceSimulateWrapsRepositoryErrors(t *testing.T) {
	repositoryError := errors.New("database unavailable")
	repository := &fakeRepository{candidatesError: repositoryError}

	_, err := NewService(
		repository,
		800,
		LinearAccessibility{},
	).Simulate(context.Background(), routeWithStops("A", "B"))
	if !errors.Is(err, repositoryError) {
		t.Fatalf("Simulate() error = %v, want wrapped repository error", err)
	}
}

type fakeRepository struct {
	candidates          []CellCandidate
	pairDemand          []StopPairDemand
	candidatesError     error
	pairDemandError     error
	receivedRoute       Route
	receivedRadius      float64
	receivedCells       []AssignedCell
	findCandidatesCalls int
}

func (repository *fakeRepository) FindCellCandidates(
	_ context.Context,
	route Route,
	radiusMeters float64,
) ([]CellCandidate, error) {
	repository.findCandidatesCalls++
	repository.receivedRoute = route
	repository.receivedRadius = radiusMeters
	return repository.candidates, repository.candidatesError
}

func (repository *fakeRepository) FindDemandByStopPair(
	_ context.Context,
	cells []AssignedCell,
) ([]StopPairDemand, error) {
	repository.receivedCells = append([]AssignedCell(nil), cells...)
	return repository.pairDemand, repository.pairDemandError
}

func routeWithStops(ids ...string) Route {
	stops := make([]Stop, len(ids))
	for index, id := range ids {
		stops[index] = Stop{ID: id}
	}
	return Route{Stops: stops}
}

func assertFloat(t *testing.T, name string, actual, expected float64) {
	t.Helper()
	if math.Abs(actual-expected) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, actual, expected)
	}
}
