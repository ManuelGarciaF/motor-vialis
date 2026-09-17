package traveltime

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestServiceUsesDifferentLocalPacesByZone(t *testing.T) {
	repository := &fakeRepository{measured: []MeasuredSegment{
		measuredSegment(0, "A", "B", 1000, []Reference{
			reference(1, 100, 1000, Paces{OffPeak: 0.10, Typical: 0.20, Peak: 0.30}),
			reference(2, 100, 1000, Paces{OffPeak: 0.10, Typical: 0.20, Peak: 0.30}),
			reference(3, 100, 1000, Paces{OffPeak: 0.10, Typical: 0.20, Peak: 0.30}),
		}),
		measuredSegment(1, "B", "C", 500, []Reference{
			reference(4, 300, 500, Paces{OffPeak: 0.40, Typical: 0.50, Peak: 0.60}),
			reference(5, 300, 500, Paces{OffPeak: 0.40, Typical: 0.50, Peak: 0.60}),
			reference(6, 300, 500, Paces{OffPeak: 0.40, Typical: 0.50, Peak: 0.60}),
		}),
	}}

	result, err := NewService(repository, testPolicy()).Estimate(
		context.Background(),
		routeWithSegmentCount(2),
	)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}

	if result.TotalDistanceMeters != 1500 {
		t.Fatalf("distance = %v, want 1500", result.TotalDistanceMeters)
	}
	if result.OffPeakSeconds != 300 ||
		result.TypicalSeconds != 450 ||
		result.PeakSeconds != 600 {
		t.Fatalf(
			"totals = %d/%d/%d, want 300/450/600",
			result.OffPeakSeconds,
			result.TypicalSeconds,
			result.PeakSeconds,
		)
	}
	if result.BySegment[0].Source != SourceLocal100 ||
		result.BySegment[0].Confidence != ConfidenceHigh {
		t.Fatalf("first segment = %#v", result.BySegment[0])
	}
	if result.BySegment[1].Source != SourceLocal300 ||
		result.BySegment[1].Confidence != ConfidenceMedium {
		t.Fatalf("second segment = %#v", result.BySegment[1])
	}
	if result.Confidence != ConfidenceMedium {
		t.Fatalf("confidence = %q, want medium", result.Confidence)
	}
}

func TestServiceExpandsRadiusUntilItHasEnoughRoutes(t *testing.T) {
	references := []Reference{
		reference(1, 100, 1000, Paces{0.1, 0.2, 0.3}),
		reference(2, 100, 1000, Paces{0.1, 0.2, 0.3}),
		reference(1, 300, 1000, Paces{0.2, 0.3, 0.4}),
		reference(2, 300, 1000, Paces{0.2, 0.3, 0.4}),
		reference(3, 300, 1000, Paces{0.2, 0.3, 0.4}),
	}
	repository := &fakeRepository{
		measured: []MeasuredSegment{
			measuredSegment(0, "A", "B", 1000, references),
		},
	}

	result, err := NewService(repository, testPolicy()).Estimate(
		context.Background(),
		routeWithSegmentCount(1),
	)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if result.BySegment[0].Source != SourceLocal300 {
		t.Fatalf("source = %q, want %q", result.BySegment[0].Source, SourceLocal300)
	}
	if result.BySegment[0].ReferenceRouteCount != 3 {
		t.Fatalf("route count = %d, want 3", result.BySegment[0].ReferenceRouteCount)
	}
}

func TestServiceConsolidatesMultipleSegmentsFromOneRoute(t *testing.T) {
	repository := &fakeRepository{
		measured: []MeasuredSegment{measuredSegment(
			0,
			"A",
			"B",
			1000,
			[]Reference{
				reference(7, 800, 500, Paces{0.1, 0.2, 0.3}),
				reference(7, 800, 500, Paces{0.3, 0.4, 0.5}),
			},
		)},
	}

	result, err := NewService(repository, testPolicy()).Estimate(
		context.Background(),
		routeWithSegmentCount(1),
	)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	segment := result.BySegment[0]
	if segment.ReferenceRouteCount != 1 {
		t.Fatalf("route count = %d, want 1", segment.ReferenceRouteCount)
	}
	if segment.Source != SourceLocal800 || segment.Confidence != ConfidenceLow {
		t.Fatalf("segment = %#v", segment)
	}
	if segment.TypicalSeconds != 300 {
		t.Fatalf("typical seconds = %d, want 300", segment.TypicalSeconds)
	}
}

func TestServiceUsesGlobalFallback(t *testing.T) {
	repository := &fakeRepository{
		measured: []MeasuredSegment{
			measuredSegment(0, "A", "B", 1000, nil),
		},
		global: GlobalPaces{
			Paces:      Paces{OffPeak: 0.1, Typical: 0.2, Peak: 0.3},
			RouteCount: 40,
		},
	}

	result, err := NewService(repository, testPolicy()).Estimate(
		context.Background(),
		routeWithSegmentCount(1),
	)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	segment := result.BySegment[0]
	if segment.Source != SourceGlobal ||
		segment.Confidence != ConfidenceLow ||
		segment.ReferenceRouteCount != 40 {
		t.Fatalf("segment = %#v", segment)
	}
	if repository.globalCalls != 1 {
		t.Fatalf("global calls = %d, want 1", repository.globalCalls)
	}
}

func TestServiceLoadsGlobalFallbackOnce(t *testing.T) {
	repository := &fakeRepository{
		measured: []MeasuredSegment{
			measuredSegment(0, "A", "B", 100, nil),
			measuredSegment(1, "B", "C", 100, nil),
		},
		global: GlobalPaces{
			Paces:      Paces{OffPeak: 0.1, Typical: 0.2, Peak: 0.3},
			RouteCount: 10,
		},
	}
	if _, err := NewService(repository, testPolicy()).Estimate(
		context.Background(),
		routeWithSegmentCount(2),
	); err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}
	if repository.globalCalls != 1 {
		t.Fatalf("global calls = %d, want 1", repository.globalCalls)
	}
}

func TestServiceRejectsInvalidRepositoryMeasurements(t *testing.T) {
	repository := &fakeRepository{
		answerVerbatim: true,
		measured: []MeasuredSegment{
			measuredSegment(1, "A", "B", 100, nil),
		},
	}
	_, err := NewService(repository, testPolicy()).Estimate(
		context.Background(),
		routeWithSegmentCount(1),
	)
	if err == nil {
		t.Fatal("Estimate() error = nil")
	}
}

func TestServiceWrapsRepositoryErrors(t *testing.T) {
	want := errors.New("database unavailable")
	repository := &fakeRepository{referencesError: want}
	_, err := NewService(repository, testPolicy()).Estimate(
		context.Background(),
		routeWithSegmentCount(1),
	)
	if !errors.Is(err, want) {
		t.Fatalf("Estimate() error = %v, want wrapped error", err)
	}
}

func TestServiceStopsQueryingOnceEverySegmentResolves(t *testing.T) {
	enough := []Reference{
		reference(1, 100, 1000, Paces{0.1, 0.2, 0.3}),
		reference(2, 100, 1000, Paces{0.1, 0.2, 0.3}),
		reference(3, 100, 1000, Paces{0.1, 0.2, 0.3}),
	}
	repository := &fakeRepository{measured: []MeasuredSegment{
		measuredSegment(0, "A", "B", 1000, enough),
		measuredSegment(1, "B", "C", 1000, enough),
	}}

	if _, err := NewService(repository, testPolicy()).Estimate(
		context.Background(),
		routeWithSegmentCount(2),
	); err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}

	want := []float64{100}
	if !reflect.DeepEqual(repository.requestedRadii, want) {
		t.Fatalf(
			"requested radii = %v, want %v: the wider corridors are discarded, "+
				"so measuring them is wasted work",
			repository.requestedRadii,
			want,
		)
	}
}

func TestServiceOnlyWidensTheSearchForUnresolvedSegments(t *testing.T) {
	repository := &fakeRepository{measured: []MeasuredSegment{
		measuredSegment(0, "A", "B", 1000, []Reference{
			reference(1, 100, 1000, Paces{0.1, 0.2, 0.3}),
			reference(2, 100, 1000, Paces{0.1, 0.2, 0.3}),
			reference(3, 100, 1000, Paces{0.1, 0.2, 0.3}),
		}),
		measuredSegment(1, "B", "C", 1000, []Reference{
			reference(4, 300, 1000, Paces{0.2, 0.3, 0.4}),
			reference(5, 300, 1000, Paces{0.2, 0.3, 0.4}),
			reference(6, 300, 1000, Paces{0.2, 0.3, 0.4}),
		}),
	}}

	result, err := NewService(repository, testPolicy()).Estimate(
		context.Background(),
		routeWithSegmentCount(2),
	)
	if err != nil {
		t.Fatalf("Estimate() error = %v", err)
	}

	wantOrders := [][]int{{0, 1}, {1}}
	if !reflect.DeepEqual(repository.requestedOrders, wantOrders) {
		t.Fatalf(
			"requested orders = %v, want %v",
			repository.requestedOrders,
			wantOrders,
		)
	}
	if result.BySegment[0].Source != SourceLocal100 ||
		result.BySegment[1].Source != SourceLocal300 {
		t.Fatalf("sources = %#v", result.BySegment)
	}
}

func TestServiceRejectsPolicyWithoutReferenceRadii(t *testing.T) {
	policy := testPolicy()
	policy.ReferenceRadiiMeters = nil
	repository := &fakeRepository{measured: []MeasuredSegment{
		measuredSegment(0, "A", "B", 1000, nil),
	}}

	_, err := NewService(repository, policy).Estimate(
		context.Background(),
		routeWithSegmentCount(1),
	)
	if err == nil {
		t.Fatal("Estimate() error = nil")
	}
	if len(repository.requestedRadii) != 0 {
		t.Fatalf("requested radii = %v, want none", repository.requestedRadii)
	}
}

func TestWeightedMedianUsesWeightsAndStableRouteOrder(t *testing.T) {
	values := []routePace{
		{RouteID: 3, Paces: Paces{Typical: 0.3}, Weight: 0.2},
		{RouteID: 1, Paces: Paces{Typical: 0.1}, Weight: 0.7},
		{RouteID: 2, Paces: Paces{Typical: 0.2}, Weight: 0.1},
	}
	actual := weightedMedian(values, func(value routePace) float64 {
		return value.Paces.Typical
	})
	if math.Abs(actual-0.1) > 1e-9 {
		t.Fatalf("weightedMedian() = %v, want 0.1", actual)
	}
}

func testPolicy() Policy {
	return Policy{
		ReferenceRadiiMeters:      []float64{100, 300, 800},
		MinimumReferenceRoutes:    3,
		DirectionToleranceDegrees: 60,
		MinimumCommercialSpeedKPH: 2,
		MaximumCommercialSpeedKPH: 80,
	}
}

type fakeRepository struct {
	measured        []MeasuredSegment
	global          GlobalPaces
	referencesError error
	globalError     error
	globalCalls     int
	requestedRadii  []float64
	requestedOrders [][]int
	answerVerbatim  bool
}

// FindSegmentReferences mirrors the repository's request-scoped results.
func (repository *fakeRepository) FindSegmentReferences(
	_ context.Context,
	segments []Segment,
	_ Policy,
	radiusMeters float64,
) ([]MeasuredSegment, error) {
	repository.requestedRadii = append(repository.requestedRadii, radiusMeters)
	orders := make([]int, 0, len(segments))
	requested := make(map[int]bool, len(segments))
	for _, segment := range segments {
		orders = append(orders, segment.Order)
		requested[segment.Order] = true
	}
	repository.requestedOrders = append(repository.requestedOrders, orders)
	if repository.referencesError != nil {
		return nil, repository.referencesError
	}
	if repository.answerVerbatim {
		return repository.measured, nil
	}

	answer := make([]MeasuredSegment, 0, len(segments))
	for _, measured := range repository.measured {
		if requested[measured.Order] {
			answer = append(answer, measured)
		}
	}
	return answer, nil
}

func (repository *fakeRepository) FindGlobalPaces(
	_ context.Context,
	_ Policy,
) (GlobalPaces, error) {
	repository.globalCalls++
	return repository.global, repository.globalError
}

func measuredSegment(
	order int,
	origin, destination string,
	length float64,
	references []Reference,
) MeasuredSegment {
	return MeasuredSegment{
		Segment: Segment{
			Order:             order,
			OriginStopID:      origin,
			DestinationStopID: destination,
		},
		LengthMeters: length,
		References:   references,
	}
}

func reference(
	routeID int64,
	radius, overlap float64,
	paces Paces,
) Reference {
	return Reference{
		RouteID:        routeID,
		RadiusMeters:   radius,
		DistanceMeters: 0,
		OverlapMeters:  overlap,
		Paces:          paces,
	}
}

func routeWithSegmentCount(count int) route.Route {
	stops := make([]route.Stop, count+1)
	for index := range stops {
		stops[index].ID = string(rune('A' + index))
		if index < count {
			stops[index].PathToNext = &route.LineString{}
		}
	}
	return route.Route{Stops: stops}
}
