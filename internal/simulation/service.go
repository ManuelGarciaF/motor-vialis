package simulation

import (
	"context"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
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

// RevenueEstimator calculates potential fare revenue for a simulated route.
type RevenueEstimator interface {
	Estimate(context.Context, route.Route, demand.Result, traveltime.Result) (revenue.Result, error)
}

// Service executes every simulation estimator.
type Service struct {
	demandEstimator     DemandEstimator
	travelTimeEstimator TravelTimeEstimator
	revenueEstimator    RevenueEstimator
}

// NewService creates the simulation orchestrator.
func NewService(
	demandEstimator DemandEstimator,
	travelTimeEstimator TravelTimeEstimator,
	revenueEstimator RevenueEstimator,
) *Service {
	return &Service{
		demandEstimator:     demandEstimator,
		travelTimeEstimator: travelTimeEstimator,
		revenueEstimator:    revenueEstimator,
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
	revenueResult, err := service.revenueEstimator.Estimate(
		ctx,
		input,
		demandResult,
		travelTimeResult,
	)
	if err != nil {
		return Result{}, fmt.Errorf("estimate revenue: %w", err)
	}

	return Result{
		Demand:  demandResult,
		Revenue: revenueResult,
		Metrics: Metrics{
			TotalDistanceMeters: travelTimeResult.TotalDistanceMeters,
			TravelTime:          travelTimeResult,
		},
		ByStop: StopContributions(input, demandResult, revenueResult),
	}, nil
}
