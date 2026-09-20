package lines_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func drawnRoute() route.Route {
	return route.Route{Stops: []route.Stop{
		{
			ID:       "a",
			Position: route.Position{Longitude: -58.38, Latitude: -34.60},
			PathToNext: &route.LineString{Positions: []route.Position{
				{Longitude: -58.38, Latitude: -34.60},
				{Longitude: -58.39, Latitude: -34.60},
			}},
		},
		{
			ID:       "b",
			Position: route.Position{Longitude: -58.39, Latitude: -34.60},
		},
	}}
}

func TestFindSimilarSendsTheFoldedPathAndTheEndpoints(t *testing.T) {
	repository := &fakeRepository{}
	service := lines.NewService(repository, testPolicy())

	if _, err := service.FindSimilar(context.Background(), lines.SimilarityRequest{
		Route: drawnRoute(),
	}); err != nil {
		t.Fatalf("FindSimilar() error = %v", err)
	}

	sent := repository.receivedSimilarity
	if len(sent.Path.Positions) != 2 {
		t.Fatalf("path = %#v, want the two folded positions", sent.Path.Positions)
	}
	if sent.Origin != (route.Position{Longitude: -58.38, Latitude: -34.60}) ||
		sent.Destination != (route.Position{Longitude: -58.39, Latitude: -34.60}) {
		t.Fatalf("endpoints = %#v / %#v", sent.Origin, sent.Destination)
	}
	if sent.CorridorToleranceMeters != 200 || sent.MinimumCoverage != 0.20 {
		t.Fatalf(
			"tolerance/minimum = %v/%v, want the policy's",
			sent.CorridorToleranceMeters,
			sent.MinimumCoverage,
		)
	}
}

func TestFindSimilarAppliesTheDefaultAndMaximumResultCount(t *testing.T) {
	cases := []struct {
		name      string
		requested int
		want      int
	}{
		{"unset falls back to the default", 0, 10},
		{"a smaller ask is honoured", 3, 3},
		{"an oversized ask is clamped", 5000, 50},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &fakeRepository{}
			service := lines.NewService(repository, testPolicy())

			found, err := service.FindSimilar(
				context.Background(),
				lines.SimilarityRequest{Route: drawnRoute(), Limit: testCase.requested},
			)
			if err != nil {
				t.Fatalf("FindSimilar() error = %v", err)
			}
			if repository.receivedSimilarity.Limit != testCase.want {
				t.Fatalf(
					"limit sent = %d, want %d",
					repository.receivedSimilarity.Limit,
					testCase.want,
				)
			}
			// The applied limit is echoed so a caller can tell a short list
			// from a truncated one.
			if found.Limit != testCase.want {
				t.Fatalf("limit reported = %d, want %d", found.Limit, testCase.want)
			}
		})
	}
}

// A drawn route with a single stop has no corridor, whatever else it carries.
func TestFindSimilarRequiresTwoStops(t *testing.T) {
	service := lines.NewService(&fakeRepository{}, testPolicy())

	_, err := service.FindSimilar(context.Background(), lines.SimilarityRequest{
		Route: route.Route{Stops: []route.Stop{{
			ID:       "a",
			Position: route.Position{Longitude: -58.38, Latitude: -34.60},
		}}},
	})

	var validationError *route.ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error = %v, want a route.ValidationError", err)
	}
	if validationError.Field != "route.stops" {
		t.Fatalf("field = %q, want route.stops", validationError.Field)
	}
}

// No fare is computed, so demanding a jurisdiction would only force the caller
// to attach a meaningless value to ask a question about geometry.
func TestFindSimilarDoesNotRequireAJurisdiction(t *testing.T) {
	service := lines.NewService(&fakeRepository{}, testPolicy())

	if _, err := service.FindSimilar(context.Background(), lines.SimilarityRequest{
		Route: drawnRoute(),
	}); err != nil {
		t.Fatalf("FindSimilar() error = %v, want none", err)
	}
}

// A sketch missing the geometry between two stops is still answerable, unlike
// the same route sent to /simulations.
func TestFindSimilarAcceptsAStopWithoutGeometry(t *testing.T) {
	repository := &fakeRepository{}
	service := lines.NewService(repository, testPolicy())

	drawn := route.Route{Stops: []route.Stop{
		{ID: "a", Position: route.Position{Longitude: -58.38, Latitude: -34.60}},
		{ID: "b", Position: route.Position{Longitude: -58.41, Latitude: -34.62}},
	}}
	if _, err := service.FindSimilar(
		context.Background(),
		lines.SimilarityRequest{Route: drawn},
	); err != nil {
		t.Fatalf("FindSimilar() error = %v, want none", err)
	}
	if len(repository.receivedSimilarity.Path.Positions) != 2 {
		t.Fatalf(
			"path = %#v, want the straight segment between the two stops",
			repository.receivedSimilarity.Path.Positions,
		)
	}
}

func TestFindSimilarRejectsAnImpossibleCoordinate(t *testing.T) {
	service := lines.NewService(&fakeRepository{}, testPolicy())

	_, err := service.FindSimilar(context.Background(), lines.SimilarityRequest{
		Route: route.Route{Stops: []route.Stop{
			{ID: "a", Position: route.Position{Longitude: -58.38, Latitude: -34.60}},
			{ID: "b", Position: route.Position{Longitude: -58.39, Latitude: 91}},
		}},
	})

	var validationError *route.ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error = %v, want a route.ValidationError", err)
	}
	if validationError.Field != "route.stops[1].position.latitude" {
		t.Fatalf("field = %q", validationError.Field)
	}
}

// The repository already ranks the rows; the service re-applies the same rule
// so the ordering cannot depend on which repository answered.
func TestFindSimilarReordersWhatTheRepositoryReturns(t *testing.T) {
	repository := &fakeRepository{similar: []lines.Similarity{
		{Line: lines.Summary{ID: 1}, CoverageOfProposed: 1.00, CoverageOfStored: 0.07},
		{Line: lines.Summary{ID: 2}, CoverageOfProposed: 0.85, CoverageOfStored: 0.80},
	}}
	service := lines.NewService(repository, testPolicy())

	found, err := service.FindSimilar(
		context.Background(),
		lines.SimilarityRequest{Route: drawnRoute()},
	)
	if err != nil {
		t.Fatalf("FindSimilar() error = %v", err)
	}
	if found.Lines[0].Line.ID != 2 {
		t.Fatalf("first = %d, want 2", found.Lines[0].Line.ID)
	}
}

// An empty shortlist is an answer — nothing runs where this route was drawn —
// and it has to serialise as [] rather than as null.
func TestFindSimilarReturnsAnEmptyListWhenNothingMatches(t *testing.T) {
	service := lines.NewService(&fakeRepository{}, testPolicy())

	found, err := service.FindSimilar(
		context.Background(),
		lines.SimilarityRequest{Route: drawnRoute()},
	)
	if err != nil {
		t.Fatalf("FindSimilar() error = %v", err)
	}
	if found.Lines == nil || len(found.Lines) != 0 {
		t.Fatalf("lines = %#v, want an empty slice", found.Lines)
	}
}
