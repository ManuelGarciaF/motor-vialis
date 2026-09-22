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
func (service *Service) Compare(
	ctx context.Context,
	input ComparisonInput,
) (Comparison, error) {
	if err := validateComparison(input); err != nil {
		return Comparison{}, err
	}

	// The shared context cancels the other database-heavy simulation on failure.
	group, groupContext := errgroup.WithContext(ctx)
	var baseline, proposed Result
	group.Go(func() error {
		var err error
		baseline, err = service.Simulate(groupContext, input.Baseline)
		if err != nil {
			return fmt.Errorf("simulate baseline: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		var err error
		proposed, err = service.Simulate(groupContext, input.Proposed)
		if err != nil {
			return fmt.Errorf("simulate proposed: %w", err)
		}
		return nil
	})
	if err := group.Wait(); err != nil {
		return Comparison{}, err
	}

	return Comparison{
		Baseline: baseline,
		Proposed: proposed,
		Delta:    newDelta(input, baseline, proposed),
	}, nil
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
