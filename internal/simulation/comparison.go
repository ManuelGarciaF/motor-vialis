package simulation

import (
	"context"
	"errors"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
	"golang.org/x/sync/errgroup"
)

// ComparisonInput contains two complete routes to evaluate.
type ComparisonInput struct {
	Baseline Route `json:"baseline"`
	Proposed Route `json:"proposed"`
}

// Comparison is both simulations together with how the proposal moved each
// metric.
type Comparison struct {
	Baseline Result `json:"baseline"`
	Proposed Result `json:"proposed"`
	Delta    Delta  `json:"delta"`
}

// Delta is the difference between the two simulations.
type Delta struct {
	StopCount IntChange    `json:"stopCount"`
	Demand    DemandDelta  `json:"demand"`
	Revenue   RevenueDelta `json:"revenue"`
	Metrics   MetricsDelta `json:"metrics"`
}

// DemandDelta is how the proposal moved the demand the route could carry.
type DemandDelta struct {
	GrossDemand     Change `json:"grossDemand"`
	PotentialDemand Change `json:"potentialDemand"`
}

// RevenueDelta is how the proposal moved the expected fare revenue.
type RevenueDelta struct {
	PotentialRevenueCents Change `json:"potentialRevenueCents"`
}

// MetricsDelta is how the proposal moved the length and duration of the route.
type MetricsDelta struct {
	TotalDistanceMeters Change          `json:"totalDistanceMeters"`
	TravelTime          TravelTimeDelta `json:"travelTime"`
}

// TravelTimeDelta is how the proposal moved commercial travel time.
type TravelTimeDelta struct {
	OffPeakSeconds IntChange        `json:"offPeakSeconds"`
	TypicalSeconds IntChange        `json:"typicalSeconds"`
	PeakSeconds    IntChange        `json:"peakSeconds"`
	Confidence     ConfidenceChange `json:"confidence"`
}

// ConfidenceChange reports both ordinal levels without deriving a numeric delta.
type ConfidenceChange struct {
	Baseline traveltime.Confidence `json:"baseline"`
	Proposed traveltime.Confidence `json:"proposed"`
}

// Compare simulates both routes and reports the difference between them.
func (service *Service) Compare(ctx context.Context, input ComparisonInput) (Comparison, error) {
	return service.compare(ctx, input, false)
}

// CompareDetour evaluates operational metrics on the detour while charging
// retained stop pairs by their distance along the original route.
func (service *Service) CompareDetour(ctx context.Context, input ComparisonInput) (Comparison, error) {
	return service.compare(ctx, input, true)
}

func (service *Service) compare(
	ctx context.Context,
	input ComparisonInput,
	detour bool,
) (Comparison, error) {
	if err := validateComparison(input); err != nil {
		return Comparison{}, err
	}
	if detour {
		if err := validateDetourStops(input); err != nil {
			return Comparison{}, err
		}
	}

	// The shared context cancels the other database-heavy estimation on failure.
	group, groupContext := errgroup.WithContext(ctx)
	var baseline, proposed estimation
	group.Go(func() error {
		var err error
		baseline, err = service.estimate(groupContext, input.Baseline)
		if err != nil {
			return fmt.Errorf("simulate baseline: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		var err error
		proposed, err = service.estimate(groupContext, input.Proposed)
		if err != nil {
			return fmt.Errorf("simulate proposed: %w", err)
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		return Comparison{}, err
	}

	fareTravelTime := proposed.travelTime
	if detour {
		var err error
		fareTravelTime, err = detourFareTravelTime(input, baseline.travelTime.BySegment)
		if err != nil {
			return Comparison{}, err
		}
	}
	baselineResult, err := service.result(ctx, input.Baseline, baseline, baseline.travelTime)
	if err != nil {
		return Comparison{}, fmt.Errorf("simulate baseline: %w", err)
	}
	proposedResult, err := service.result(ctx, input.Proposed, proposed, fareTravelTime)
	if err != nil {
		return Comparison{}, fmt.Errorf("simulate proposed: %w", err)
	}
	return newComparison(input, baselineResult, proposedResult), nil
}

func validateDetourStops(input ComparisonInput) error {
	baselineOrder := make(map[string]int, len(input.Baseline.Stops))
	for order, stop := range input.Baseline.Stops {
		baselineOrder[stop.ID] = order
	}
	previous := -1
	for _, stop := range input.Proposed.Stops {
		order, found := baselineOrder[stop.ID]
		if !found || order <= previous {
			return fmt.Errorf("detour stops must be an ordered subset of baseline stops")
		}
		previous = order
	}
	return nil
}

func detourFareTravelTime(
	input ComparisonInput,
	baselineSegments []traveltime.SegmentResult,
) (traveltime.Result, error) {
	if len(baselineSegments) != len(input.Baseline.Stops)-1 {
		return traveltime.Result{}, fmt.Errorf("baseline travel time has %d segments, want %d", len(baselineSegments), len(input.Baseline.Stops)-1)
	}
	baselineOrder := make(map[string]int, len(input.Baseline.Stops))
	for order, stop := range input.Baseline.Stops {
		baselineOrder[stop.ID] = order
	}
	result := traveltime.Result{BySegment: make([]traveltime.SegmentResult, 0, len(input.Proposed.Stops)-1)}
	for order := 0; order < len(input.Proposed.Stops)-1; order++ {
		origin := input.Proposed.Stops[order]
		destination := input.Proposed.Stops[order+1]
		from := baselineOrder[origin.ID]
		to := baselineOrder[destination.ID]
		var distance float64
		for index := from; index < to; index++ {
			segment := baselineSegments[index]
			if segment.OriginStopID != input.Baseline.Stops[index].ID ||
				segment.DestinationStopID != input.Baseline.Stops[index+1].ID {
				return traveltime.Result{}, fmt.Errorf("baseline travel-time segments do not match baseline route")
			}
			distance += segment.DistanceMeters
		}
		result.BySegment = append(result.BySegment, traveltime.SegmentResult{
			OriginStopID: origin.ID, DestinationStopID: destination.ID, DistanceMeters: distance,
		})
	}
	return result, nil
}

func newComparison(input ComparisonInput, baseline, proposed Result) Comparison {
	return Comparison{
		Baseline: baseline,
		Proposed: proposed,
		Delta:    newDelta(input, baseline, proposed),
	}
}

// validateComparison rejects invalid routes before database work begins.
func validateComparison(input ComparisonInput) error {
	if err := rootedValidation(input.Baseline, "baseline"); err != nil {
		return err
	}
	if err := rootedValidation(input.Proposed, "proposed"); err != nil {
		return err
	}

	// Revenue from different tariff authorities is not comparable.
	if input.Baseline.Jurisdiction != input.Proposed.Jurisdiction {
		return &route.ValidationError{
			Field: "proposed.jurisdiction",
			Message: "must match the baseline jurisdiction: results calculated " +
				"under different tariff authorities are not comparable",
		}
	}
	return nil
}

func rootedValidation(input Route, root string) error {
	err := route.Validate(input)
	if err == nil {
		return nil
	}
	var validationError *route.ValidationError
	if errors.As(err, &validationError) {
		return validationError.Rooted(root)
	}
	return err
}

func newDelta(input ComparisonInput, baseline, proposed Result) Delta {
	baselineGlobal := baseline.Global
	proposedGlobal := proposed.Global
	return Delta{
		StopCount: newIntChange(
			int64(len(input.Baseline.Stops)),
			int64(len(input.Proposed.Stops)),
		),
		Demand: DemandDelta{
			GrossDemand: newChange(
				baselineGlobal.Demand.GrossDemand,
				proposedGlobal.Demand.GrossDemand,
			),
			PotentialDemand: newChange(
				baselineGlobal.Demand.PotentialDemand,
				proposedGlobal.Demand.PotentialDemand,
			),
		},
		Revenue: RevenueDelta{
			PotentialRevenueCents: newChange(
				baselineGlobal.Revenue.PotentialRevenueCents,
				proposedGlobal.Revenue.PotentialRevenueCents,
			),
		},
		Metrics: MetricsDelta{
			TotalDistanceMeters: newChange(
				baselineGlobal.Metrics.TotalDistanceMeters,
				proposedGlobal.Metrics.TotalDistanceMeters,
			),
			TravelTime: TravelTimeDelta{
				OffPeakSeconds: newIntChange(
					baselineGlobal.Metrics.TravelTime.OffPeakSeconds,
					proposedGlobal.Metrics.TravelTime.OffPeakSeconds,
				),
				TypicalSeconds: newIntChange(
					baselineGlobal.Metrics.TravelTime.TypicalSeconds,
					proposedGlobal.Metrics.TravelTime.TypicalSeconds,
				),
				PeakSeconds: newIntChange(
					baselineGlobal.Metrics.TravelTime.PeakSeconds,
					proposedGlobal.Metrics.TravelTime.PeakSeconds,
				),
				Confidence: ConfidenceChange{
					Baseline: baselineGlobal.Metrics.TravelTime.Confidence,
					Proposed: proposedGlobal.Metrics.TravelTime.Confidence,
				},
			},
		},
	}
}
