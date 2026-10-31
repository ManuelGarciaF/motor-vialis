package detour

import (
	"context"
	"errors"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

const ErrorNoCutPath ErrorCode = "no_cut_path"

// CutPathInput asks for the street path a drawn cut follows through ordered
// waypoints. A cut closes streets, it is not a trip: every street can be
// followed in both directions and turn restrictions do not apply, so a cut
// drawn against a one-way carriageway stays on that carriageway.
type CutPathInput struct {
	Waypoints []route.Position
}

// CutPathPolicy bounds the cost of one cut path request. The local graph is
// the waypoints' bounding box grown by MarginMeters, widened once to
// RetryMarginMeters when no path exists inside it.
type CutPathPolicy struct {
	MaximumWaypoints   int
	MaximumSpanMeters  float64
	MarginMeters       float64
	RetryMarginMeters  float64
	SnapDistanceMeters float64
}

// CutPathRequest is one routing attempt over a bounded local graph.
type CutPathRequest struct {
	Waypoints          []route.Position
	MarginMeters       float64
	SnapDistanceMeters float64
}

// CutPathRouter finds the undirected street path through the waypoints.
type CutPathRouter interface {
	RouteCutPath(context.Context, CutPathRequest) (SegmentRoute, error)
}

// CutPathService resolves the streets a drawn cut runs along.
type CutPathService struct {
	router CutPathRouter
	policy CutPathPolicy
}

// NewCutPathService creates a cut path router.
func NewCutPathService(router CutPathRouter, policy CutPathPolicy) (*CutPathService, error) {
	if router == nil {
		return nil, fmt.Errorf("cut path router must not be nil")
	}
	if policy.MaximumWaypoints < 2 ||
		policy.MaximumSpanMeters <= 0 ||
		policy.MarginMeters <= 0 ||
		policy.RetryMarginMeters < policy.MarginMeters ||
		policy.SnapDistanceMeters <= 0 {
		return nil, &ValidationError{Field: "policy", Message: "contains incoherent cut path limits"}
	}
	return &CutPathService{router: router, policy: policy}, nil
}

// Route finds the path through the waypoints. Like a segment, a first attempt
// uses a tight graph around the waypoints and is retried once over a wider
// graph only when no path exists there.
func (service *CutPathService) Route(ctx context.Context, input CutPathInput) (SegmentRoute, error) {
	if err := validateWaypoints(
		input.Waypoints,
		service.policy.MaximumWaypoints,
		service.policy.MaximumSpanMeters,
	); err != nil {
		return SegmentRoute{}, err
	}

	request := CutPathRequest{
		Waypoints:          input.Waypoints,
		MarginMeters:       service.policy.MarginMeters,
		SnapDistanceMeters: service.policy.SnapDistanceMeters,
	}
	result, err := service.router.RouteCutPath(ctx, request)
	var domainError *Error
	if err != nil && errors.As(err, &domainError) && domainError.Code == ErrorNoCutPath {
		request.MarginMeters = service.policy.RetryMarginMeters
		return service.router.RouteCutPath(ctx, request)
	}
	return result, err
}
