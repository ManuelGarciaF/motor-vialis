package detour

import (
	"context"
	"errors"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestCutPathServiceValidatesWaypointsBeforeRouting(t *testing.T) {
	origin := route.Position{Latitude: -34.60, Longitude: -58.40}
	tooMany := make([]route.Position, 51)
	for index := range tooMany {
		tooMany[index] = route.Position{Latitude: -34.60, Longitude: -58.40 + float64(index)*0.0001}
	}

	cases := []struct {
		name      string
		waypoints []route.Position
		wantField string
	}{
		{"one waypoint", []route.Position{origin}, "waypoints"},
		{"too many waypoints", tooMany, "waypoints"},
		{"invalid latitude", []route.Position{origin, {Latitude: -91, Longitude: -58.39}}, "waypoints[1].latitude"},
		{"same position", []route.Position{origin, origin}, "waypoints"},
		{"too long", []route.Position{origin, {Latitude: -34.70, Longitude: -58.40}}, "waypoints"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router := &scriptedCutPathRouter{}
			service := newTestCutPathService(t, router)

			_, err := service.Route(context.Background(), CutPathInput{Waypoints: testCase.waypoints})

			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error = %v, want *ValidationError", err)
			}
			if validationError.Field != testCase.wantField {
				t.Fatalf("field = %q, want %q", validationError.Field, testCase.wantField)
			}
			if len(router.requests) != 0 {
				t.Fatalf("router ran %d times for invalid waypoints", len(router.requests))
			}
		})
	}
}

func TestCutPathServiceWidensTheGraphOnceWhenNoPathExists(t *testing.T) {
	noPath := &Error{Code: ErrorNoCutPath, Kind: ErrorKindNoSolution}
	cases := []struct {
		name        string
		errs        []error
		wantMargins []float64
		wantErr     error
	}{
		{"first attempt succeeds", []error{nil}, []float64{300}, nil},
		{"retry succeeds", []error{noPath, nil}, []float64{300, 1000}, nil},
		{"retry fails too", []error{noPath, noPath}, []float64{300, 1000}, noPath},
		{
			"unmatched waypoint is not retried",
			[]error{&Error{Code: ErrorWaypointNotMatched, Kind: ErrorKindNoSolution}},
			[]float64{300},
			&Error{Code: ErrorWaypointNotMatched},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router := &scriptedCutPathRouter{errs: testCase.errs}
			service := newTestCutPathService(t, router)
			waypoints := []route.Position{
				{Latitude: -34.600, Longitude: -58.400},
				{Latitude: -34.600, Longitude: -58.390},
			}

			_, err := service.Route(context.Background(), CutPathInput{Waypoints: waypoints})

			var margins []float64
			for _, request := range router.requests {
				margins = append(margins, request.MarginMeters)
				if request.SnapDistanceMeters != 50 || len(request.Waypoints) != len(waypoints) {
					t.Fatalf("request = %#v", request)
				}
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

func TestNewCutPathServiceRejectsIncoherentLimits(t *testing.T) {
	policy := testCutPathPolicy()
	policy.RetryMarginMeters = policy.MarginMeters - 1

	if _, err := NewCutPathService(&scriptedCutPathRouter{}, policy); err == nil {
		t.Fatal("NewCutPathService() accepted a retry margin smaller than the first one")
	}
}

type scriptedCutPathRouter struct {
	errs     []error
	requests []CutPathRequest
}

func (router *scriptedCutPathRouter) RouteCutPath(
	_ context.Context,
	request CutPathRequest,
) (SegmentRoute, error) {
	call := len(router.requests)
	router.requests = append(router.requests, request)
	if call < len(router.errs) && router.errs[call] != nil {
		return SegmentRoute{}, router.errs[call]
	}
	return SegmentRoute{DistanceMeters: 100}, nil
}

func newTestCutPathService(t *testing.T, router CutPathRouter) *CutPathService {
	t.Helper()
	service, err := NewCutPathService(router, testCutPathPolicy())
	if err != nil {
		t.Fatalf("NewCutPathService() error = %v", err)
	}
	return service
}

func testCutPathPolicy() CutPathPolicy {
	return CutPathPolicy{
		MaximumWaypoints:   50,
		MaximumSpanMeters:  10_000,
		MarginMeters:       300,
		RetryMarginMeters:  1000,
		SnapDistanceMeters: 50,
	}
}
