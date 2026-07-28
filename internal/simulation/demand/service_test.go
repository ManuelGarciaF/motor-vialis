package demand

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestServiceEstimateAssignsCellsAndBuildsTotals(t *testing.T) {
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
	input := routeWithStops("A", "B", "C")

	result, err := service.Estimate(context.Background(), input)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}

	if repository.receivedRadius != 800 {
		t.Fatalf("radius = %v, want 800", repository.receivedRadius)
	}
	if !reflect.DeepEqual(repository.receivedRoute, input) {
		t.Fatalf("route = %#v, want %#v", repository.receivedRoute, input)
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

	if _, err := service.Estimate(
		context.Background(),
		routeWithStops("A", "B"),
	); err != nil {
		t.Fatalf("Estimate() error = %v", err)
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

func TestServiceEstimateWrapsRepositoryErrors(t *testing.T) {
	repositoryError := errors.New("database unavailable")
	repository := &fakeRepository{candidatesError: repositoryError}

	_, err := NewService(
		repository,
		800,
		LinearAccessibility{},
	).Estimate(context.Background(), routeWithStops("A", "B"))
	if !errors.Is(err, repositoryError) {
		t.Fatalf("Estimate() error = %v, want wrapped repository error", err)
	}
}

type fakeRepository struct {
	candidates      []CellCandidate
	pairDemand      []StopPairDemand
	candidatesError error
	pairDemandError error
	receivedRoute   route.Route
	receivedRadius  float64
	receivedCells   []AssignedCell
}

func (repository *fakeRepository) FindCellCandidates(
	_ context.Context,
	input route.Route,
	radiusMeters float64,
) ([]CellCandidate, error) {
	repository.receivedRoute = input
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

func routeWithStops(ids ...string) route.Route {
	stops := make([]route.Stop, len(ids))
	for index, id := range ids {
		stops[index] = route.Stop{ID: id}
	}
	return route.Route{Stops: stops}
}

func assertFloat(t *testing.T, name string, actual, expected float64) {
	t.Helper()
	if math.Abs(actual-expected) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, actual, expected)
	}
}
