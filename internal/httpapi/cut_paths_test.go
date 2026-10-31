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

type fakeCutPathRouter struct {
	result   detour.SegmentRoute
	err      error
	received detour.CutPathInput
	calls    int
}

func (router *fakeCutPathRouter) Route(
	_ context.Context,
	input detour.CutPathInput,
) (detour.SegmentRoute, error) {
	router.calls++
	router.received = input
	return router.result, router.err
}

func newCutPathTestRouter(router httpapi.CutPathRouter) http.Handler {
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
		handler = handler.WithCutPathRouter(router)
	}
	return handler.Routes()
}

const cutPathBody = `{
	"waypoints": [
		{"latitude": -34.5616, "longitude": -58.4431},
		{"latitude": -34.5623, "longitude": -58.4414},
		{"latitude": -34.5631, "longitude": -58.4377}
	]
}`

func TestCreateCutPathReturnsGeometryAndDistance(t *testing.T) {
	router := &fakeCutPathRouter{result: detour.SegmentRoute{
		Geometry: route.LineString{Positions: []route.Position{
			{Latitude: -34.5616, Longitude: -58.4431},
			{Latitude: -34.5631, Longitude: -58.4377},
		}},
		DistanceMeters: 530.2,
	}}

	response := doRequest(t, newCutPathTestRouter(router), "/routes/cut", []byte(cutPathBody))

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
		got.Geometry.Coordinates[0] != [2]float64{-58.4431, -34.5616} {
		t.Fatalf("geometry = %#v", got.Geometry)
	}
	if got.DistanceMeters != 530.2 {
		t.Fatalf("distance = %v", got.DistanceMeters)
	}
	waypoints := router.received.Waypoints
	if len(waypoints) != 3 || waypoints[2] != (route.Position{Latitude: -34.5631, Longitude: -58.4377}) {
		t.Fatalf("router received = %#v", router.received)
	}
}

func TestCreateCutPathReportsErrors(t *testing.T) {
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
			body:       `{"waypoints": [], "avoid": null}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_error",
		},
		{
			name:       "invalid waypoint",
			body:       cutPathBody,
			err:        &detour.ValidationError{Field: "waypoints[0].longitude", Message: "must be between -180 and 180"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "validation_error",
			wantField:  "waypoints[0].longitude",
			wantCalls:  1,
		},
		{
			name:       "no path",
			body:       cutPathBody,
			err:        &detour.Error{Code: detour.ErrorNoCutPath, Kind: detour.ErrorKindNoSolution, Message: "no street path"},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "no_cut_path",
			wantCalls:  1,
		},
		{
			name: "waypoint far from every street",
			body: cutPathBody,
			err: &detour.Error{
				Code: detour.ErrorWaypointNotMatched, Kind: detour.ErrorKindNoSolution,
				Field: "waypoints[1]", Message: "waypoint 1 is more than 50 m from any street",
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "waypoint_not_matched",
			wantField:  "waypoints[1]",
			wantCalls:  1,
		},
		{
			name:       "timeout",
			body:       cutPathBody,
			err:        fmt.Errorf("create cut path graph: %w", context.DeadlineExceeded),
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "timeout",
			wantCalls:  1,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			router := &fakeCutPathRouter{err: testCase.err}

			response := doRequest(t, newCutPathTestRouter(router), "/routes/cut", []byte(testCase.body))

			assertErrorResponse(t, response, testCase.wantStatus, testCase.wantCode, testCase.wantField)
			if router.calls != testCase.wantCalls {
				t.Fatalf("router calls = %d, want %d", router.calls, testCase.wantCalls)
			}
		})
	}
}

func TestCreateCutPathWithoutRouterIsUnavailable(t *testing.T) {
	response := doRequest(t, newCutPathTestRouter(nil), "/routes/cut", []byte(cutPathBody))

	assertErrorResponse(t, response, http.StatusServiceUnavailable, "cut_path_unavailable", "")
}
