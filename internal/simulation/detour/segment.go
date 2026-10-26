package detour

import (
	"context"
	"errors"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

const (
	ErrorWaypointNotMatched ErrorCode = "waypoint_not_matched"
	ErrorNoSegmentRoute     ErrorCode = "no_segment_route"
)

// SegmentInput asks for the street path through ordered waypoints. The first
// and last waypoints are the stops the segment joins; the ones in between are
// optional via points. Avoid, when present, is a cut the path must not cross.
// A bearing, when present, only lets its waypoint snap to a street travelled
// in roughly that direction.
type SegmentInput struct {
	Waypoints          []route.Position
	Avoid              *Cut
	OriginBearing      *float64
	DestinationBearing *float64
}

// SegmentRoute is the routed path from the first to the last waypoint.
type SegmentRoute struct {
	Geometry       route.LineString
	DistanceMeters float64
}

// SegmentPolicy bounds the cost of one segment request. The local graph is
// the waypoints' bounding box grown by MarginMeters, widened once to
// RetryMarginMeters when no path exists inside it.
type SegmentPolicy struct {
	MaximumWaypoints            int
	MaximumSpanMeters           float64
	MarginMeters                float64
	RetryMarginMeters           float64
	SnapDistanceMeters          float64
	UnboundedSnapDegrees        float64
	BearingSnapToleranceDegrees float64
}

// SegmentRoutingRequest is one routing attempt over a bounded local graph.
type SegmentRoutingRequest struct {
	Points       []RoutingPoint
	Avoid        *Cut
	MarginMeters float64
}

// SegmentRouter routes consecutive waypoint pairs with the detour graph.
type SegmentRouter interface {
	RouteSegment(context.Context, SegmentRoutingRequest, Policy) (SegmentRoute, error)
}

// SegmentService routes single route segments around an optional cut with the
// same graph, cut blocking, snapping, and turn restrictions RF05 uses.
type SegmentService struct {
	router        SegmentRouter
	policy        Policy
	segmentPolicy SegmentPolicy
}

// NewSegmentService creates a segment router.
func NewSegmentService(router SegmentRouter, policy Policy, segmentPolicy SegmentPolicy) (*SegmentService, error) {
	if router == nil {
		return nil, fmt.Errorf("segment router must not be nil")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	if segmentPolicy.MaximumWaypoints < 2 ||
		segmentPolicy.MaximumSpanMeters <= 0 ||
		segmentPolicy.MarginMeters <= 0 ||
		segmentPolicy.RetryMarginMeters < segmentPolicy.MarginMeters ||
		segmentPolicy.SnapDistanceMeters <= 0 ||
		segmentPolicy.UnboundedSnapDegrees <= 0 ||
		segmentPolicy.BearingSnapToleranceDegrees <= 0 {
		return nil, &ValidationError{Field: "policy", Message: "contains incoherent segment limits"}
	}
	return &SegmentService{router: router, policy: policy, segmentPolicy: segmentPolicy}, nil
}

// Route finds the path through the waypoints. A first attempt uses a tight
// graph around the waypoints; only when no path exists there is it retried
// once over a wider graph.
func (service *SegmentService) Route(ctx context.Context, input SegmentInput) (SegmentRoute, error) {
	if err := ValidateSegmentInput(input, service.segmentPolicy); err != nil {
		return SegmentRoute{}, err
	}
	if input.Avoid != nil {
		if err := ValidateCut(*input.Avoid, service.policy); err != nil {
			return SegmentRoute{}, err
		}
	}

	request := SegmentRoutingRequest{
		Points:       service.segmentPoints(input),
		Avoid:        input.Avoid,
		MarginMeters: service.segmentPolicy.MarginMeters,
	}
	result, err := service.router.RouteSegment(ctx, request, service.policy)
	var domainError *Error
	if err != nil && errors.As(err, &domainError) && domainError.Code == ErrorNoSegmentRoute {
		request.MarginMeters = service.segmentPolicy.RetryMarginMeters
		return service.router.RouteSegment(ctx, request, service.policy)
	}
	return result, err
}

func (service *SegmentService) segmentPoints(input SegmentInput) []RoutingPoint {
	points := make([]RoutingPoint, len(input.Waypoints))
	last := len(input.Waypoints) - 1
	for index, position := range input.Waypoints {
		point := RoutingPoint{
			ID:                        int64(index + 1),
			Position:                  position,
			MaximumDistanceMeters:     service.segmentPolicy.SnapDistanceMeters,
			Role:                      PointRequiredStop,
			DirectionToleranceDegrees: service.segmentPolicy.UnboundedSnapDegrees,
		}
		var bearing *float64
		switch index {
		case 0:
			bearing = input.OriginBearing
		case last:
			bearing = input.DestinationBearing
		}
		if bearing != nil {
			point.DirectionDegrees = *bearing
			point.DirectionToleranceDegrees = service.segmentPolicy.BearingSnapToleranceDegrees
		}
		points[index] = point
	}
	return points
}

// ValidateSegmentInput checks every limit that does not need the database.
func ValidateSegmentInput(input SegmentInput, policy SegmentPolicy) error {
	if len(input.Waypoints) < 2 {
		return &ValidationError{Field: "waypoints", Message: "must contain at least two positions"}
	}
	if len(input.Waypoints) > policy.MaximumWaypoints {
		return &ValidationError{
			Field:   "waypoints",
			Message: fmt.Sprintf("must contain at most %d positions", policy.MaximumWaypoints),
		}
	}

	var span float64
	for index, position := range input.Waypoints {
		field := fmt.Sprintf("waypoints[%d]", index)
		if !finite(position.Latitude) || position.Latitude < -90 || position.Latitude > 90 {
			return &ValidationError{Field: field + ".latitude", Message: "must be between -90 and 90"}
		}
		if !finite(position.Longitude) || position.Longitude < -180 || position.Longitude > 180 {
			return &ValidationError{Field: field + ".longitude", Message: "must be between -180 and 180"}
		}
		if index > 0 {
			span += route.DistanceMeters(input.Waypoints[index-1], position)
		}
	}
	if span == 0 {
		return &ValidationError{Field: "waypoints", Message: "must not all be the same position"}
	}
	if span > policy.MaximumSpanMeters {
		return &ValidationError{
			Field:   "waypoints",
			Message: fmt.Sprintf("must not span more than %.0f meters in straight lines", policy.MaximumSpanMeters),
		}
	}

	for _, bearing := range []struct {
		field string
		value *float64
	}{
		{"originBearing", input.OriginBearing},
		{"destinationBearing", input.DestinationBearing},
	} {
		if bearing.value != nil && (!finite(*bearing.value) || *bearing.value < 0 || *bearing.value >= 360) {
			return &ValidationError{Field: bearing.field, Message: "must be at least 0 and less than 360 degrees"}
		}
	}
	return nil
}

// JoinSegmentPaths concatenates per-pair paths, dropping repeated junction
// positions, and measures the result geodesically.
func JoinSegmentPaths(paths []route.LineString) SegmentRoute {
	var positions []route.Position
	for _, path := range paths {
		for _, position := range path.Positions {
			if count := len(positions); count > 0 && positions[count-1] == position {
				continue
			}
			positions = append(positions, position)
		}
	}

	var distance float64
	for index := 1; index < len(positions); index++ {
		distance += route.DistanceMeters(positions[index-1], positions[index])
	}
	return SegmentRoute{Geometry: route.LineString{Positions: positions}, DistanceMeters: distance}
}
