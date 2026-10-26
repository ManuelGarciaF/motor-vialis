package detour

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestValidateSegmentInputReportsTheInvalidField(t *testing.T) {
	origin := route.Position{Latitude: -34.60, Longitude: -58.40}
	destination := route.Position{Latitude: -34.60, Longitude: -58.39}
	bearing := func(value float64) *float64 { return &value }
	tooMany := make([]route.Position, 26)
	for index := range tooMany {
		tooMany[index] = route.Position{Latitude: -34.60, Longitude: -58.40 + float64(index)*0.0001}
	}

	cases := []struct {
		name      string
		input     SegmentInput
		wantField string
	}{
		{"one waypoint", SegmentInput{Waypoints: []route.Position{origin}}, "waypoints"},
		{"too many waypoints", SegmentInput{Waypoints: tooMany}, "waypoints"},
		{"invalid latitude", SegmentInput{Waypoints: []route.Position{origin, {Latitude: 91, Longitude: -58.39}}}, "waypoints[1].latitude"},
		{"invalid longitude", SegmentInput{Waypoints: []route.Position{{Latitude: -34.6, Longitude: math.NaN()}, destination}}, "waypoints[0].longitude"},
		{"same position", SegmentInput{Waypoints: []route.Position{origin, origin}}, "waypoints"},
		{"too long", SegmentInput{Waypoints: []route.Position{origin, {Latitude: -34.70, Longitude: -58.40}}}, "waypoints"},
		{"origin bearing", SegmentInput{Waypoints: []route.Position{origin, destination}, OriginBearing: bearing(360)}, "originBearing"},
		{"destination bearing", SegmentInput{Waypoints: []route.Position{origin, destination}, DestinationBearing: bearing(-1)}, "destinationBearing"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateSegmentInput(testCase.input, testSegmentPolicy())

			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error = %v, want *ValidationError", err)
			}
			if validationError.Field != testCase.wantField {
				t.Fatalf("field = %q, want %q", validationError.Field, testCase.wantField)
			}
		})
	}
}

func TestSegmentServiceSnapsOnlyBearingWaypointsByDirection(t *testing.T) {
	router := &scriptedSegmentRouter{}
	service := newTestSegmentService(t, router)
	bearing := 90.0

	_, err := service.Route(context.Background(), SegmentInput{
		Waypoints: []route.Position{
			{Latitude: -34.600, Longitude: -58.400},
			{Latitude: -34.601, Longitude: -58.395},
			{Latitude: -34.600, Longitude: -58.390},
		},
		OriginBearing: &bearing,
	})
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}

	points := router.requests[0].Points
	if len(points) != 3 {
		t.Fatalf("points = %d, want 3", len(points))
	}
	if points[0].DirectionDegrees != 90 || points[0].DirectionToleranceDegrees != 60 {
		t.Fatalf("origin point = %#v, want bearing 90 with the policy tolerance", points[0])
	}
	for _, point := range points[1:] {
		if point.DirectionToleranceDegrees != 180 {
			t.Fatalf("point %d tolerance = %v, want 180 without a bearing", point.ID, point.DirectionToleranceDegrees)
		}
	}
	if router.requests[0].MarginMeters != 300 || router.requests[0].Avoid != nil {
		t.Fatalf("request = %#v", router.requests[0])
	}
}

func TestSegmentServiceWidensTheGraphOnceWhenNoPathExists(t *testing.T) {
	noRoute := &Error{Code: ErrorNoSegmentRoute, Kind: ErrorKindNoSolution}
	cases := []struct {
		name        string
		errs        []error
		wantMargins []float64
		wantErr     error
	}{
		{"first attempt succeeds", []error{nil}, []float64{300}, nil},
		{"retry succeeds", []error{noRoute, nil}, []float64{300, 1000}, nil},
		{"retry fails too", []error{noRoute, noRoute}, []float64{300, 1000}, noRoute},
		{
			"unmatched waypoint is not retried",
			[]error{&Error{Code: ErrorWaypointNotMatched, Kind: ErrorKindNoSolution}},
			[]float64{300},
			&Error{Code: ErrorWaypointNotMatched},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router := &scriptedSegmentRouter{errs: testCase.errs}
			service := newTestSegmentService(t, router)

			_, err := service.Route(context.Background(), SegmentInput{
				Waypoints: []route.Position{
					{Latitude: -34.600, Longitude: -58.400},
					{Latitude: -34.600, Longitude: -58.390},
				},
				Avoid: &Cut{Lines: []route.LineString{{Positions: []route.Position{
					{Latitude: -34.6005, Longitude: -58.395},
					{Latitude: -34.5995, Longitude: -58.395},
				}}}},
			})

			var margins []float64
			for _, request := range router.requests {
				margins = append(margins, request.MarginMeters)
			}
			if len(margins) != len(testCase.wantMargins) {
				t.Fatalf("margins = %v, want %v", margins, testCase.wantMargins)
			}
			for index := range margins {
				if margins[index] != testCase.wantMargins[index] {
					t.Fatalf("margins = %v, want %v", margins, testCase.wantMargins)
				}
			}
			var want *Error
			if errors.As(testCase.wantErr, &want) {
				var got *Error
				if !errors.As(err, &got) || got.Code != want.Code {
					t.Fatalf("error = %v, want code %s", err, want.Code)
				}
			} else if err != nil {
				t.Fatalf("Route() error = %v", err)
			}
		})
	}
}

func TestSegmentServiceRejectsAnInvalidCutBeforeRouting(t *testing.T) {
	router := &scriptedSegmentRouter{}
	service := newTestSegmentService(t, router)

	_, err := service.Route(context.Background(), SegmentInput{
		Waypoints: []route.Position{
			{Latitude: -34.600, Longitude: -58.400},
			{Latitude: -34.600, Longitude: -58.390},
		},
		Avoid: &Cut{Lines: []route.LineString{{Positions: []route.Position{{Latitude: -34.6, Longitude: -58.395}}}}},
	})

	var domainError *Error
	if !errors.As(err, &domainError) || domainError.Code != ErrorInvalidCut {
		t.Fatalf("error = %v, want invalid_cut", err)
	}
	if len(router.requests) != 0 {
		t.Fatalf("router ran %d times for an invalid cut", len(router.requests))
	}
}

func TestJoinSegmentPathsDropsRepeatedJunctions(t *testing.T) {
	a := route.Position{Latitude: -34.600, Longitude: -58.400}
	b := route.Position{Latitude: -34.600, Longitude: -58.399}
	c := route.Position{Latitude: -34.600, Longitude: -58.398}

	joined := JoinSegmentPaths([]route.LineString{
		{Positions: []route.Position{a, b, b}},
		{Positions: []route.Position{b, c}},
	})

	if len(joined.Geometry.Positions) != 3 {
		t.Fatalf("positions = %#v, want a, b, c", joined.Geometry.Positions)
	}
	want := route.DistanceMeters(a, b) + route.DistanceMeters(b, c)
	if math.Abs(joined.DistanceMeters-want) > 1e-9 {
		t.Fatalf("distance = %v, want %v", joined.DistanceMeters, want)
	}
}

type scriptedSegmentRouter struct {
	errs     []error
	requests []SegmentRoutingRequest
}

func (router *scriptedSegmentRouter) RouteSegment(
	_ context.Context,
	request SegmentRoutingRequest,
	_ Policy,
) (SegmentRoute, error) {
	call := len(router.requests)
	router.requests = append(router.requests, request)
	if call < len(router.errs) && router.errs[call] != nil {
		return SegmentRoute{}, router.errs[call]
	}
	return SegmentRoute{DistanceMeters: 100}, nil
}

func newTestSegmentService(t *testing.T, router SegmentRouter) *SegmentService {
	t.Helper()
	service, err := NewSegmentService(router, testPolicy(), testSegmentPolicy())
	if err != nil {
		t.Fatalf("NewSegmentService() error = %v", err)
	}
	return service
}

func testSegmentPolicy() SegmentPolicy {
	return SegmentPolicy{
		MaximumWaypoints:            25,
		MaximumSpanMeters:           10_000,
		MarginMeters:                300,
		RetryMarginMeters:           1000,
		SnapDistanceMeters:          50,
		UnboundedSnapDegrees:        180,
		BearingSnapToleranceDegrees: 60,
	}
}
