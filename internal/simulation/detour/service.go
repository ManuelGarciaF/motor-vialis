package detour

import (
	"context"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// Service validates a detour request and selects a path through the road graph.
type Service struct {
	repository Repository
	policy     Policy
}

// NewService creates the detour domain service.
func NewService(repository Repository, policy Policy) *Service {
	return &Service{repository: repository, policy: policy}
}

// Plan validates one request, verifies graph coverage, classifies its stops,
// and selects the best available ordered path.
func (service *Service) Plan(ctx context.Context, input Input) (Plan, error) {
	if err := route.Validate(input.Route); err != nil {
		return Plan{}, err
	}
	if err := validateCriterion(input.Criterion); err != nil {
		return Plan{}, err
	}
	if err := ValidateCut(input.Cut, service.policy); err != nil {
		return Plan{}, err
	}
	if service.repository == nil {
		return Plan{}, fmt.Errorf("plan detour: repository is nil")
	}

	intersects, err := service.repository.CutIntersectsGraph(ctx, input.Cut)
	if err != nil {
		return Plan{}, fmt.Errorf("check cut against graph coverage: %w", err)
	}
	if !intersects {
		return Plan{}, &Error{
			Code:    ErrorCutOutsideGraph,
			Kind:    ErrorKindInvalidInput,
			Message: "cut does not intersect the active road graph coverage",
		}
	}

	stops := ClassifyStops(input.Route, input.Cut, service.policy)
	connections, err := service.repository.FindConnections(ctx, RoutingInput{
		Route: input.Route,
		Cut:   input.Cut,
		Stops: append([]ClassifiedStop(nil), stops...),
	})
	if err != nil {
		return Plan{}, fmt.Errorf("find detour connections: %w", err)
	}
	selection, err := Select(stops, connections, input.Criterion)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Stops: stops, Selection: selection}, nil
}
