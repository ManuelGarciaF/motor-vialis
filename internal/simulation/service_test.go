package simulation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

func TestServiceSimulateBuildsNestedResult(t *testing.T) {
	input := validRoute()
	demandResult := demand.Result{
		GrossDemand:     100,
		PotentialDemand: 75,
		ByStopPair: []demand.StopPairDemand{
			{OriginStopID: "A", DestinationStopID: "B", PotentialDemand: 75},
		},
	}
	timeResult := traveltime.Result{
		TotalDistanceMeters: 1200,
		OffPeakSeconds:      200,
		TypicalSeconds:      240,
		PeakSeconds:         300,
		Confidence:          traveltime.ConfidenceHigh,
	}
	demandEstimator := &fakeDemandEstimator{result: demandResult}
	timeEstimator := &fakeTravelTimeEstimator{result: timeResult}
	revenueResult := revenue.Result{
		Jurisdiction:          route.JurisdictionCABA,
		PotentialRevenueCents: 1234,
	}
	revenueEstimator := &fakeRevenueEstimator{result: revenueResult}

	result, err := NewService(demandEstimator, timeEstimator, revenueEstimator).Simulate(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	wantGlobal := GlobalResult{
		Demand: DemandTotals{GrossDemand: 100, PotentialDemand: 75},
		Revenue: RevenueTotals{
			Jurisdiction:          route.JurisdictionCABA,
			PotentialRevenueCents: 1234,
		},
		Metrics: MetricsTotals{
			TotalDistanceMeters: 1200,
			TravelTime: TravelTimeTotals{
				OffPeakSeconds: 200,
				TypicalSeconds: 240,
				PeakSeconds:    300,
				Confidence:     traveltime.ConfidenceHigh,
			},
		},
	}
	if !reflect.DeepEqual(result.Global, wantGlobal) {
		t.Fatalf("global = %#v, want %#v", result.Global, wantGlobal)
	}
	if !reflect.DeepEqual(demandEstimator.received, input) ||
		!reflect.DeepEqual(timeEstimator.received, input) {
		t.Fatal("estimators did not receive the validated route")
	}

	if len(result.ByStop) != len(input.Stops) {
		t.Fatalf("byStop = %d entries, want %d", len(result.ByStop), len(input.Stops))
	}
	if result.ByStop[0].Demand.OriginPotential != 75 ||
		result.ByStop[1].Demand.DestinationPotential != 75 {
		t.Fatalf("byStop = %#v", result.ByStop)
	}
}

// Internal estimator detail must not leak into the API result.
func TestServiceResultOmitsPerPairDetail(t *testing.T) {
	result, err := NewService(
		&fakeDemandEstimator{result: demand.Result{
			PotentialDemand: 10,
			ByStopPair: []demand.StopPairDemand{
				{OriginStopID: "A", DestinationStopID: "B", PotentialDemand: 10},
			},
		}},
		&fakeTravelTimeEstimator{result: traveltime.Result{
			BySegment: []traveltime.SegmentResult{{OriginStopID: "A"}},
		}},
		&fakeRevenueEstimator{result: revenue.Result{
			ByStopPair: []revenue.StopPairResult{{OriginStopID: "A"}},
		}},
	).Simulate(context.Background(), validRoute())
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, field := range []string{"byStopPair", "bySegment"} {
		if bytes.Contains(encoded, []byte(field)) {
			t.Fatalf("encoded result contains %q: %s", field, encoded)
		}
	}
}

func TestServiceRejectsInvalidRouteBeforeEstimators(t *testing.T) {
	input := validRoute()
	input.Stops[0].PathToNext = nil
	demandEstimator := &fakeDemandEstimator{}
	timeEstimator := &fakeTravelTimeEstimator{}
	revenueEstimator := &fakeRevenueEstimator{}

	_, err := NewService(demandEstimator, timeEstimator, revenueEstimator).Simulate(
		context.Background(),
		input,
	)
	if err == nil {
		t.Fatal("Simulate() error = nil")
	}
	var validationError *ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if demandEstimator.calls != 0 || timeEstimator.calls != 0 {
		t.Fatal("an estimator was called for invalid input")
	}
}

func TestServiceStopsWhenDemandFails(t *testing.T) {
	want := errors.New("demand unavailable")
	demandEstimator := &fakeDemandEstimator{err: want}
	timeEstimator := &fakeTravelTimeEstimator{}
	revenueEstimator := &fakeRevenueEstimator{}

	_, err := NewService(demandEstimator, timeEstimator, revenueEstimator).Simulate(
		context.Background(),
		validRoute(),
	)
	if !errors.Is(err, want) {
		t.Fatalf("Simulate() error = %v, want wrapped error", err)
	}
	if timeEstimator.calls != 0 {
		t.Fatal("travel-time estimator was called after demand failed")
	}
}

func TestServiceWrapsTravelTimeErrors(t *testing.T) {
	want := errors.New("travel-time unavailable")
	_, err := NewService(
		&fakeDemandEstimator{},
		&fakeTravelTimeEstimator{err: want},
		&fakeRevenueEstimator{},
	).Simulate(context.Background(), validRoute())
	if !errors.Is(err, want) {
		t.Fatalf("Simulate() error = %v, want wrapped error", err)
	}
}

func TestServiceWrapsRevenueErrors(t *testing.T) {
	want := errors.New("revenue unavailable")
	_, err := NewService(
		&fakeDemandEstimator{}, &fakeTravelTimeEstimator{}, &fakeRevenueEstimator{err: want},
	).Simulate(context.Background(), validRoute())
	if !errors.Is(err, want) {
		t.Fatalf("Simulate() error = %v, want wrapped error", err)
	}
}

type fakeDemandEstimator struct {
	result   demand.Result
	err      error
	received route.Route
	calls    int
}

func (estimator *fakeDemandEstimator) Estimate(
	_ context.Context,
	input route.Route,
) (demand.Result, error) {
	estimator.calls++
	estimator.received = input
	return estimator.result, estimator.err
}

type fakeTravelTimeEstimator struct {
	result   traveltime.Result
	err      error
	received route.Route
	calls    int
}

type fakeRevenueEstimator struct {
	result revenue.Result
	err    error
}

func (estimator *fakeRevenueEstimator) Estimate(_ context.Context, _ route.Route, _ demand.Result, _ traveltime.Result) (revenue.Result, error) {
	return estimator.result, estimator.err
}

func (estimator *fakeTravelTimeEstimator) Estimate(
	_ context.Context,
	input route.Route,
) (traveltime.Result, error) {
	estimator.calls++
	estimator.received = input
	return estimator.result, estimator.err
}

func validRoute() route.Route {
	origin := route.Position{Latitude: -34.6000, Longitude: -58.3800}
	destination := route.Position{Latitude: -34.6010, Longitude: -58.3810}
	return route.Route{Jurisdiction: route.JurisdictionCABA, Stops: []route.Stop{
		{
			ID:       "A",
			Position: origin,
			PathToNext: &route.LineString{Positions: []route.Position{
				origin,
				destination,
			}},
		},
		{ID: "B", Position: destination},
	}}
}
