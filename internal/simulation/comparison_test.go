package simulation

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

func TestCompareReportsTheDifferenceBetweenBothRoutes(t *testing.T) {
	service := NewService(
		&scriptedDemandEstimator{},
		&scriptedTravelTimeEstimator{},
		&scriptedRevenueEstimator{},
	)

	comparison, err := service.Compare(context.Background(), ComparisonInput{
		Baseline: validRoute(),
		Proposed: validRoute(),
	})
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}

	if comparison.Delta.Demand.PotentialDemand.Absolute != 0 {
		t.Fatalf("potential demand delta = %#v", comparison.Delta.Demand.PotentialDemand)
	}
	if comparison.Delta.Metrics.TravelTime.Confidence.Baseline != traveltime.ConfidenceHigh ||
		comparison.Delta.Metrics.TravelTime.Confidence.Proposed != traveltime.ConfidenceHigh {
		t.Fatalf("confidence = %#v", comparison.Delta.Metrics.TravelTime.Confidence)
	}
	if comparison.Baseline.Global != comparison.Proposed.Global {
		t.Fatal("identical routes produced different results")
	}
}

func TestCompareMeasuresEachMetricAgainstTheBaseline(t *testing.T) {
	proposed := validRoute()
	proposed.Stops = append(proposed.Stops, route.Stop{
		ID:       "C",
		Position: route.Position{Latitude: -34.6020, Longitude: -58.3820},
	})
	proposed.Stops[1].PathToNext = &route.LineString{Positions: []route.Position{
		proposed.Stops[1].Position,
		proposed.Stops[2].Position,
	}}
	service := NewService(
		&scriptedDemandEstimator{proposedPotential: 150},
		&scriptedTravelTimeEstimator{proposedTypicalSeconds: 360},
		&scriptedRevenueEstimator{proposedCents: 2000},
	)

	comparison, err := service.Compare(context.Background(), ComparisonInput{
		Baseline: validRoute(),
		Proposed: proposed,
	})
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}

	stopCount := comparison.Delta.StopCount
	if stopCount.Baseline != 2 || stopCount.Proposed != 3 || stopCount.Absolute != 1 {
		t.Fatalf("stop count = %#v", stopCount)
	}
	potential := comparison.Delta.Demand.PotentialDemand
	if potential.Absolute != 50 ||
		potential.Relative == nil ||
		math.Abs(*potential.Relative-0.5) > 1e-9 {
		t.Fatalf("potential demand = %#v", potential)
	}
	typical := comparison.Delta.Metrics.TravelTime.TypicalSeconds
	if typical.Baseline != 240 || typical.Proposed != 360 || typical.Absolute != 120 {
		t.Fatalf("typical seconds = %#v", typical)
	}
	revenueDelta := comparison.Delta.Revenue.PotentialRevenueCents
	if revenueDelta.Absolute != 1000 {
		t.Fatalf("revenue = %#v", revenueDelta)
	}
}

func TestCompareDetourUsesOriginalDistanceForProposedFare(t *testing.T) {
	baseline := threeStopRoute()
	proposed := route.Route{Jurisdiction: baseline.Jurisdiction, Stops: []route.Stop{
		baseline.Stops[0],
		baseline.Stops[2],
	}}
	proposed.Stops[0].PathToNext = &route.LineString{Positions: []route.Position{
		proposed.Stops[0].Position,
		{Latitude: -34.59, Longitude: -58.39},
		proposed.Stops[1].Position,
	}}
	maximum := int64(2500)
	service := NewService(
		detourDemandEstimator{},
		detourTravelTimeEstimator{},
		revenue.NewService(detourTariffRepository{bands: []revenue.TariffBand{
			{MinimumDistanceMeters: 0, MaximumDistanceMeters: &maximum, RegisteredFareCents: 100, UnregisteredFareCents: 100},
			{MinimumDistanceMeters: 2500, RegisteredFareCents: 200, UnregisteredFareCents: 200},
		}}, revenue.Policy{CaptureFactor: 1, RegisteredCardShare: 1}),
	)

	comparison, err := service.CompareDetour(context.Background(), ComparisonInput{
		Baseline: baseline,
		Proposed: proposed,
	})
	if err != nil {
		t.Fatalf("CompareDetour() error = %v", err)
	}
	if comparison.Proposed.Global.Metrics.TotalDistanceMeters != 3000 {
		t.Fatalf("proposed distance = %v, want 3000", comparison.Proposed.Global.Metrics.TotalDistanceMeters)
	}
	if comparison.Proposed.Global.Revenue.PotentialRevenueCents != 100 {
		t.Fatalf("proposed revenue = %v, want original-distance fare 100", comparison.Proposed.Global.Revenue.PotentialRevenueCents)
	}
}

func TestCompareReportsWhichRouteIsInvalid(t *testing.T) {
	invalid := validRoute()
	invalid.Stops[0].PathToNext = nil

	cases := []struct {
		name      string
		input     ComparisonInput
		wantField string
	}{
		{
			name:      "baseline",
			input:     ComparisonInput{Baseline: invalid, Proposed: validRoute()},
			wantField: "baseline.stops[0].pathToNext",
		},
		{
			name:      "proposed",
			input:     ComparisonInput{Baseline: validRoute(), Proposed: invalid},
			wantField: "proposed.stops[0].pathToNext",
		},
		{
			name:      "both invalid reports the baseline",
			input:     ComparisonInput{Baseline: invalid, Proposed: invalid},
			wantField: "baseline.stops[0].pathToNext",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			demandEstimator := &scriptedDemandEstimator{}
			service := NewService(
				demandEstimator,
				&scriptedTravelTimeEstimator{},
				&scriptedRevenueEstimator{},
			)

			_, err := service.Compare(context.Background(), testCase.input)

			var validationError *route.ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error = %v, want *route.ValidationError", err)
			}
			if validationError.Field != testCase.wantField {
				t.Fatalf("field = %q, want %q", validationError.Field, testCase.wantField)
			}
			if demandEstimator.callCount() != 0 {
				t.Fatalf("estimator ran %d times for invalid input", demandEstimator.callCount())
			}
		})
	}
}

func TestCompareRejectsDifferentJurisdictions(t *testing.T) {
	proposed := validRoute()
	proposed.Jurisdiction = route.JurisdictionProvince
	demandEstimator := &scriptedDemandEstimator{}

	_, err := NewService(
		demandEstimator,
		&scriptedTravelTimeEstimator{},
		&scriptedRevenueEstimator{},
	).Compare(context.Background(), ComparisonInput{
		Baseline: validRoute(),
		Proposed: proposed,
	})

	var validationError *route.ValidationError
	if !errors.As(err, &validationError) {
		t.Fatalf("error = %v, want *route.ValidationError", err)
	}
	if validationError.Field != "proposed.jurisdiction" {
		t.Fatalf("field = %q, want %q", validationError.Field, "proposed.jurisdiction")
	}
	if demandEstimator.callCount() != 0 {
		t.Fatalf(
			"estimator ran %d times for mismatched jurisdictions",
			demandEstimator.callCount(),
		)
	}
}

// A failed simulation must cancel its concurrent peer.
func TestCompareCancelsTheSurvivingSimulation(t *testing.T) {
	blocked := make(chan struct{})
	failure := errors.New("demand unavailable")
	service := NewService(
		&scriptedDemandEstimator{failProposed: failure},
		&scriptedTravelTimeEstimator{blockUntilCancelled: blocked},
		&scriptedRevenueEstimator{},
	)

	done := make(chan error, 1)
	go func() {
		_, err := service.Compare(context.Background(), ComparisonInput{
			Baseline: validRoute(),
			Proposed: validRoute(),
		})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, failure) {
			t.Fatalf("Compare() error = %v, want wrapped %v", err, failure)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Compare() did not return: the surviving simulation was not cancelled")
	}
	close(blocked)
}

func TestComparisonDeltaAlwaysEncodesToJSON(t *testing.T) {
	service := NewService(
		&scriptedDemandEstimator{},
		&scriptedTravelTimeEstimator{},
		&scriptedRevenueEstimator{},
	)
	comparison, err := service.Compare(context.Background(), ComparisonInput{
		Baseline: validRoute(),
		Proposed: validRoute(),
	})
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}

	if _, err := json.Marshal(comparison); err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
}

// scriptedComparisonServices is concurrency-safe because comparisons run in parallel.
type scriptedDemandEstimator struct {
	proposedPotential float64
	failProposed      error
	mutex             sync.Mutex
	calls             int
}

func (estimator *scriptedDemandEstimator) Estimate(
	_ context.Context,
	input route.Route,
) (demand.Result, error) {
	estimator.mutex.Lock()
	estimator.calls++
	call := estimator.calls
	estimator.mutex.Unlock()

	if estimator.failProposed != nil && call > 1 {
		return demand.Result{}, estimator.failProposed
	}
	potential := 100.0
	if len(input.Stops) > 2 && estimator.proposedPotential != 0 {
		potential = estimator.proposedPotential
	}
	return demand.Result{GrossDemand: potential * 2, PotentialDemand: potential}, nil
}

func (estimator *scriptedDemandEstimator) callCount() int {
	estimator.mutex.Lock()
	defer estimator.mutex.Unlock()
	return estimator.calls
}

type scriptedTravelTimeEstimator struct {
	proposedTypicalSeconds int64
	blockUntilCancelled    chan struct{}
}

func (estimator *scriptedTravelTimeEstimator) Estimate(
	ctx context.Context,
	input route.Route,
) (traveltime.Result, error) {
	if estimator.blockUntilCancelled != nil {
		select {
		case <-ctx.Done():
			return traveltime.Result{}, ctx.Err()
		case <-estimator.blockUntilCancelled:
		}
	}
	typical := int64(240)
	if len(input.Stops) > 2 && estimator.proposedTypicalSeconds != 0 {
		typical = estimator.proposedTypicalSeconds
	}
	return traveltime.Result{
		TotalDistanceMeters: 1000,
		OffPeakSeconds:      typical - 40,
		TypicalSeconds:      typical,
		PeakSeconds:         typical + 60,
		Confidence:          traveltime.ConfidenceHigh,
	}, nil
}

type detourDemandEstimator struct{}

func (detourDemandEstimator) Estimate(_ context.Context, input route.Route) (demand.Result, error) {
	last := len(input.Stops) - 1
	return demand.Result{PotentialDemand: 1, ByStopPair: []demand.StopPairDemand{{
		OriginStopOrder: 0, OriginStopID: input.Stops[0].ID,
		DestinationStopOrder: last, DestinationStopID: input.Stops[last].ID,
		PotentialDemand: 1,
	}}}, nil
}

type detourTravelTimeEstimator struct{}

func (detourTravelTimeEstimator) Estimate(_ context.Context, input route.Route) (traveltime.Result, error) {
	segmentDistance := 1000.0
	if len(input.Stops) == 2 {
		segmentDistance = 3000
	}
	result := traveltime.Result{Confidence: traveltime.ConfidenceHigh}
	for order := 0; order < len(input.Stops)-1; order++ {
		result.TotalDistanceMeters += segmentDistance
		result.BySegment = append(result.BySegment, traveltime.SegmentResult{
			OriginStopID: input.Stops[order].ID, DestinationStopID: input.Stops[order+1].ID,
			DistanceMeters: segmentDistance,
		})
	}
	return result, nil
}

type detourTariffRepository struct {
	bands []revenue.TariffBand
}

func (repository detourTariffRepository) FindTariffBands(context.Context, route.Jurisdiction) ([]revenue.TariffBand, error) {
	return repository.bands, nil
}

func threeStopRoute() route.Route {
	positions := []route.Position{
		{Latitude: -34.600, Longitude: -58.380},
		{Latitude: -34.601, Longitude: -58.381},
		{Latitude: -34.602, Longitude: -58.382},
	}
	return route.Route{Jurisdiction: route.JurisdictionCABA, Stops: []route.Stop{
		{ID: "A", Position: positions[0], PathToNext: &route.LineString{Positions: positions[:2]}},
		{ID: "B", Position: positions[1], PathToNext: &route.LineString{Positions: positions[1:]}},
		{ID: "C", Position: positions[2]},
	}}
}

type scriptedRevenueEstimator struct {
	proposedCents float64
}

func (estimator *scriptedRevenueEstimator) Estimate(
	_ context.Context,
	input route.Route,
	_ demand.Result,
	_ traveltime.Result,
) (revenue.Result, error) {
	cents := 1000.0
	if len(input.Stops) > 2 && estimator.proposedCents != 0 {
		cents = estimator.proposedCents
	}
	return revenue.Result{
		Jurisdiction:          input.Jurisdiction,
		PotentialRevenueCents: cents,
	}, nil
}
