package simulation

import (
	"context"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// DemandEstimator calculates potential demand for a validated route.
type DemandEstimator interface {
	Estimate(ctx context.Context, input route.Route) (demand.Result, error)
}

// TravelTimeEstimator measures the route and estimates commercial travel time.
type TravelTimeEstimator interface {
	Estimate(ctx context.Context, input route.Route) (traveltime.Result, error)
}

// Service executes every simulation estimator.
type Service struct {
	demandEstimator     DemandEstimator
	travelTimeEstimator TravelTimeEstimator
}

// NewService creates the simulation orchestrator.
func NewService(
	demandEstimator DemandEstimator,
	travelTimeEstimator TravelTimeEstimator,
) *Service {
	return &Service{
		demandEstimator:     demandEstimator,
		travelTimeEstimator: travelTimeEstimator,
	}
}

// Simulate validates and calculates all metrics for one ordered route.
func (service *Service) Simulate(
	ctx context.Context,
	input route.Route,
) (Result, error) {
	if err := route.Validate(input); err != nil {
		return Result{}, err
	}

	demandResult, err := service.demandEstimator.Estimate(ctx, input)
	if err != nil {
		return Result{}, fmt.Errorf("estimate demand: %w", err)
	}
	travelTimeResult, err := service.travelTimeEstimator.Estimate(ctx, input)
	if err != nil {
		return Result{}, fmt.Errorf("estimate travel time: %w", err)
	}

	return Result{
		Demand: demandResult,
		Metrics: Metrics{
			TotalDistanceMeters: travelTimeResult.TotalDistanceMeters,
			TravelTime:          travelTimeResult,
		},
	}, nil
}
