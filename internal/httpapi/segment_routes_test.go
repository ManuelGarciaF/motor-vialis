package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/httpapi"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

type fakeSegmentRouter struct {
	result   detour.SegmentRoute
	err      error
	received detour.SegmentInput
	calls    int
}

func (router *fakeSegmentRouter) Route(
	_ context.Context,
	input detour.SegmentInput,
) (detour.SegmentRoute, error) {
	router.calls++
	router.received = input
	return router.result, router.err
}

func newSegmentTestRouter(router httpapi.SegmentRouter) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := httpapi.NewHandler(
		logger,
		&fakeSimulator{},
		&fakeComparator{},
		&fakeDetourPlanner{},
		&fakeLines{},
		&fakeTransfers{},
		5*time.Second,
	)
	if router != nil {
		handler = handler.WithSegmentRouter(router)
	}
	return handler.Routes()
}

const segmentBody = `{
	"waypoints": [
		{"latitude": -34.600, "longitude": -58.400},
		{"latitude": -34.601, "longitude": -58.395},
		{"latitude": -34.600, "longitude": -58.390}
	],
	"avoid": {"type": "LineString", "coordinates": [[-58.3951, -34.6005], [-58.3949, -34.5995]]},
	"originBearing": 90
}`

func TestCreateSegmentRouteReturnsGeometryAndDistance(t *testing.T) {
	router := &fakeSegmentRouter{result: detour.SegmentRoute{
		Geometry: route.LineString{Positions: []route.Position{
			{Latitude: -34.600, Longitude: -58.400},
			{Latitude: -34.600, Longitude: -58.390},
		}},
		DistanceMeters: 917.5,
	}}

	response := doRequest(t, newSegmentTestRouter(router), "/routes/segment", []byte(segmentBody))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var got struct {
		Geometry struct {
			Type        string       `json:"type"`
			Coordinates [][2]float64 `json:"coordinates"`
		} `json:"geometry"`
		DistanceMeters float64 `json:"distanceMeters"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Geometry.Type != "LineString" || len(got.Geometry.Coordinates) != 2 ||
		got.Geometry.Coordinates[0] != [2]float64{-58.400, -34.600} {
		t.Fatalf("geometry = %#v", got.Geometry)
	}
	if got.DistanceMeters != 917.5 {
		t.Fatalf("distance = %v", got.DistanceMeters)
	}
	received := router.received
	if len(received.Waypoints) != 3 || received.Avoid == nil || len(received.Avoid.Lines) != 1 ||
		received.OriginBearing == nil || *received.OriginBearing != 90 || received.DestinationBearing != nil {
		t.Fatalf("router received = %#v", received)
	}
}

func TestCreateSegmentRouteWithoutAvoidPassesNoCut(t *testing.T) {
	router := &fakeSegmentRouter{}
	body := `{"waypoints": [{"latitude": -34.6, "longitude": -58.4}, {"latitude": -34.6, "longitude": -58.39}]}`

	response := doRequest(t, newSegmentTestRouter(router), "/routes/segment", []byte(body))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if router.received.Avoid != nil {
		t.Fatalf("avoid = %#v, want nil", router.received.Avoid)
	}
}

func TestCreateSegmentRouteReportsErrors(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		err        error
		wantStatus int
		wantCode   string
		wantField  string
		wantCalls  int
	}{
		{
			name:       "missing waypoints",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_error",
			wantField:  "waypoints",
		},
		{
			name:       "unknown field",
			body:       `{"waypoints": [], "profile": "bus"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_error",
		},
		{
			name:       "invalid waypoint",
			body:       segmentBody,
			err:        &detour.ValidationError{Field: "waypoints[1].latitude", Message: "must be between -90 and 90"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_error",
			wantField:  "waypoints[1].latitude",
			wantCalls:  1,
		},
		{
			name:       "no route around the cut",
			body:       segmentBody,
			err:        &detour.Error{Code: detour.ErrorNoSegmentRoute, Kind: detour.ErrorKindNoSolution, Message: "no street path"},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "no_segment_route",
			wantCalls:  1,
		},
		{
			name: "unmatched waypoint",
			body: segmentBody,
			err: &detour.Error{
				Code: detour.ErrorWaypointNotMatched, Kind: detour.ErrorKindNoSolution,
				Field: "waypoints[2]", Message: "waypoint 2 is not next to a usable street, or lies on the cut",
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "waypoint_not_matched",
			wantField:  "waypoints[2]",
			wantCalls:  1,
		},
		{
			name:       "timeout",
			body:       segmentBody,
			err:        fmt.Errorf("create segment graph: %w", context.DeadlineExceeded),
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "timeout",
			wantCalls:  1,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router := &fakeSegmentRouter{err: testCase.err}

			response := doRequest(t, newSegmentTestRouter(router), "/routes/segment", []byte(testCase.body))

			assertErrorResponse(t, response, testCase.wantStatus, testCase.wantCode, testCase.wantField)
			if router.calls != testCase.wantCalls {
				t.Fatalf("router calls = %d, want %d", router.calls, testCase.wantCalls)
			}
		})
	}
}

func TestCreateSegmentRouteWithoutRouterIsUnavailable(t *testing.T) {
	response := doRequest(t, newSegmentTestRouter(nil), "/routes/segment", []byte(segmentBody))

	assertErrorResponse(t, response, http.StatusServiceUnavailable, "segment_routing_unavailable", "")
}
