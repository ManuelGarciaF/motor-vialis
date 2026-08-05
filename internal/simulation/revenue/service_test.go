package revenue

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

func TestServiceEstimateAppliesCaptureAndPaymentMix(t *testing.T) {
	maximum := int64(3000)
	service := NewService(fakeRepository{bands: []TariffBand{{
		MinimumDistanceMeters: 0, MaximumDistanceMeters: &maximum,
		RegisteredFareCents: 100, UnregisteredFareCents: 200,
	}}}, Policy{CaptureFactor: 0.5, RegisteredCardShare: 0.8})

	result, err := service.Estimate(context.Background(), route.Route{Jurisdiction: route.JurisdictionCABA}, demand.Result{
		ByStopPair: []demand.StopPairDemand{{
			OriginStopOrder: 0, OriginStopID: "A", DestinationStopOrder: 2, DestinationStopID: "C", PotentialDemand: 10,
		}},
	}, traveltime.Result{BySegment: []traveltime.SegmentResult{
		{OriginStopID: "A", DestinationStopID: "B", DistanceMeters: 1000},
		{OriginStopID: "B", DestinationStopID: "C", DistanceMeters: 1500},
	}})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if len(result.ByStopPair) != 1 {
		t.Fatalf("pairs = %d, want 1", len(result.ByStopPair))
	}
	pair := result.ByStopPair[0]
	assertNear(t, "captured demand", pair.CapturedDemand, 5)
	assertNear(t, "weighted fare", pair.WeightedFareCents, 120)
	assertNear(t, "revenue", pair.PotentialRevenueCents, 600)
	assertNear(t, "total revenue", result.PotentialRevenueCents, 600)
}

func TestServiceEstimateUsesUpperBandAtBoundary(t *testing.T) {
	maximum := int64(3000)
	service := NewService(fakeRepository{bands: []TariffBand{
		{MinimumDistanceMeters: 0, MaximumDistanceMeters: &maximum, RegisteredFareCents: 100, UnregisteredFareCents: 100},
		{MinimumDistanceMeters: 3000, RegisteredFareCents: 200, UnregisteredFareCents: 200},
	}}, Policy{CaptureFactor: 1, RegisteredCardShare: 1})
	result, err := service.Estimate(context.Background(), route.Route{Jurisdiction: route.JurisdictionCABA}, demand.Result{ByStopPair: []demand.StopPairDemand{{
		OriginStopOrder: 0, OriginStopID: "A", DestinationStopOrder: 1, DestinationStopID: "B", PotentialDemand: 1,
	}}}, traveltime.Result{BySegment: []traveltime.SegmentResult{{OriginStopID: "A", DestinationStopID: "B", DistanceMeters: 3000}}})
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if result.ByStopPair[0].RegisteredFareCents != 200 {
		t.Fatalf("fare = %d, want 200", result.ByStopPair[0].RegisteredFareCents)
	}
}

func TestServiceEstimateRejectsInvalidPolicyAndRepositoryErrors(t *testing.T) {
	service := NewService(fakeRepository{}, Policy{CaptureFactor: 1.1, RegisteredCardShare: 1})
	if _, err := service.Estimate(context.Background(), route.Route{}, demand.Result{}, traveltime.Result{}); err == nil {
		t.Fatal("Estimate() error = nil")
	}
	want := errors.New("database unavailable")
	service = NewService(fakeRepository{err: want}, Policy{CaptureFactor: 1, RegisteredCardShare: 1})
	if _, err := service.Estimate(context.Background(), route.Route{Jurisdiction: route.JurisdictionCABA}, demand.Result{}, traveltime.Result{}); !errors.Is(err, want) {
		t.Fatalf("Estimate() error = %v, want wrapped error", err)
	}
}

type fakeRepository struct {
	bands []TariffBand
	err   error
}

func (repository fakeRepository) FindTariffBands(_ context.Context, _ route.Jurisdiction) ([]TariffBand, error) {
	return repository.bands, repository.err
}

func assertNear(t *testing.T, name string, actual, want float64) {
	t.Helper()
	if math.Abs(actual-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, actual, want)
	}
}
