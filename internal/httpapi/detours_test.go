package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestCreateDetourReturnsVariantComparisonUncoveredStopsAndTrace(t *testing.T) {
	planner := &fakeDetourPlanner{result: detour.Result{
		Variant: validRouteValue(t),
		Uncovered: []detour.UncoveredStop{{
			StopOrder:      1,
			StopID:         "B",
			Availability:   detour.StopForcedUnavailable,
			DistanceMeters: 4.5,
			Baseline:       simulation.StopResult{StopOrder: 1, StopID: "B"},
		}},
		Comparison: simulation.Comparison{
			Delta: simulation.Delta{StopCount: simulation.IntChange{Absolute: -1}},
		},
		Trace: detour.Trace{
			Criterion:             detour.CriterionShortestTime,
			DecisionTravelSeconds: 125.5,
			GraphLoadID:           3,
			BlockedStreetIDs:      []int64{10, 11},
			SelectedEdgeIDs:       []int64{20, 21},
			Traffic: detour.TrafficTrace{
				FetchedAt:  time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
				TrafficAge: 90 * time.Second,
				Zoom:       14,
				TileCount:  4,
				CacheHits:  2,
			},
			Graph:        detour.GraphTrace{CandidateEdges: 100, BlockedEdges: 2},
			ForcedStops:  1,
			OmittedStops: 1,
		},
	}}
	router := newTestRouterWithDetours(
		&fakeSimulator{},
		&fakeComparator{},
		planner,
		&fakeLines{},
	)

	response := doDetourRequest(t, router, detourJSON())

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var got struct {
		Variant        route.Route `json:"variant"`
		UncoveredStops []struct {
			StopOrder           int                   `json:"stopOrder"`
			StopID              string                `json:"stopId"`
			Reason              string                `json:"reason"`
			DistanceToCutMeters float64               `json:"distanceToCutMeters"`
			Baseline            simulation.StopResult `json:"baseline"`
		} `json:"uncoveredStops"`
		Comparison simulation.Comparison `json:"comparison"`
		Trace      struct {
			Decision struct {
				Criterion            detour.Criterion `json:"criterion"`
				TrafficTravelSeconds float64          `json:"trafficTravelSeconds"`
			} `json:"decision"`
			GraphLoadID int64 `json:"graphLoadId"`
			Traffic     struct {
				TrafficAgeSeconds float64 `json:"trafficAgeSeconds"`
			} `json:"traffic"`
		} `json:"trace"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Variant.Jurisdiction != route.JurisdictionCABA ||
		got.Comparison.Delta.StopCount.Absolute != -1 {
		t.Fatalf("response variant/comparison = %#v", got)
	}
	if len(got.UncoveredStops) != 1 ||
		got.UncoveredStops[0].Reason != "cut_reached" ||
		got.UncoveredStops[0].Baseline.StopID != "B" {
		t.Fatalf("uncoveredStops = %#v", got.UncoveredStops)
	}
	if got.Trace.Decision.Criterion != detour.CriterionShortestTime ||
		got.Trace.Decision.TrafficTravelSeconds != 125.5 ||
		got.Trace.GraphLoadID != 3 || got.Trace.Traffic.TrafficAgeSeconds != 90 {
		t.Fatalf("trace = %#v", got.Trace)
	}
	if planner.received.Criterion != detour.CriterionShortestTime ||
		planner.received.Route.Jurisdiction != route.JurisdictionCABA ||
		len(planner.received.Cut.LineString.Positions) != 2 {
		t.Fatalf("planner received = %#v", planner.received)
	}
}

func TestCreateDetourReportsCriterionOmission(t *testing.T) {
	planner := &fakeDetourPlanner{result: detour.Result{
		Variant: validRouteValue(t),
		Uncovered: []detour.UncoveredStop{{
			StopOrder:    0,
			StopID:       "A",
			Availability: detour.StopOptional,
		}},
	}}
	router := newTestRouterWithDetours(
		&fakeSimulator{}, &fakeComparator{}, planner, &fakeLines{},
	)

	response := doDetourRequest(t, router, detourJSON())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var got struct {
		Uncovered []struct {
			Reason string `json:"reason"`
		} `json:"uncoveredStops"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Uncovered) != 1 || got.Uncovered[0].Reason != "criterion_omission" {
		t.Fatalf("uncoveredStops = %#v", got.Uncovered)
	}
}

func TestCreateDetourRequiresEveryField(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{name: "route", body: `{"cut":{"type":"LineString","coordinates":[[-58.38,-34.60],[-58.39,-34.61]]},"criterion":"MENOR_TIEMPO"}`, wantField: "route"},
		{name: "cut", body: fmt.Sprintf(`{"route":%s,"criterion":"MENOR_TIEMPO"}`, validRouteJSON()), wantField: "cut"},
		{name: "criterion", body: fmt.Sprintf(`{"route":%s,"cut":{"type":"LineString","coordinates":[[-58.38,-34.60],[-58.39,-34.61]]}}`, validRouteJSON()), wantField: "criterion"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			planner := &fakeDetourPlanner{}
			router := newTestRouterWithDetours(
				&fakeSimulator{}, &fakeComparator{}, planner, &fakeLines{},
			)
			response := doDetourRequest(t, router, []byte(testCase.body))
			assertErrorResponse(t, response, http.StatusBadRequest, "validation_error", testCase.wantField)
			if planner.calls != 0 {
				t.Fatalf("planner calls = %d, want 0", planner.calls)
			}
		})
	}
}

func TestCreateDetourRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	cases := [][]byte{
		[]byte(fmt.Sprintf(`{"route":%s,"cut":{"type":"LineString","coordinates":[[-58.38,-34.60],[-58.39,-34.61]]},"criterion":"MENOR_TIEMPO","extra":true}`, validRouteJSON())),
		append(detourJSON(), []byte(` {}`)...),
		[]byte(fmt.Sprintf(`{"route":%s,"cut":{"type":"LineString","coordinates":[[-58.38,-34.60],[-58.39,-34.61]],"extra":true},"criterion":"MENOR_TIEMPO"}`, validRouteJSON())),
	}
	for index, body := range cases {
		planner := &fakeDetourPlanner{}
		router := newTestRouterWithDetours(
			&fakeSimulator{}, &fakeComparator{}, planner, &fakeLines{},
		)
		response := doDetourRequest(t, router, body)
		assertErrorResponse(t, response, http.StatusBadRequest, "validation_error", "")
		if planner.calls != 0 {
			t.Fatalf("case %d planner calls = %d, want 0", index, planner.calls)
		}
	}
}

func TestCreateDetourMapsDomainErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{
			name: "criterion validation", err: &detour.ValidationError{Field: "criterion", Message: "invalid"},
			wantStatus: http.StatusBadRequest, wantCode: "validation_error", wantField: "criterion",
		},
		{
			name: "invalid cut", err: &detour.Error{Code: detour.ErrorInvalidCut, Kind: detour.ErrorKindInvalidInput, Message: "invalid"},
			wantStatus: http.StatusBadRequest, wantCode: "invalid_cut",
		},
		{
			name: "route not affected", err: &detour.Error{Code: detour.ErrorRouteNotAffected, Kind: detour.ErrorKindNoSolution, Message: "unaffected"},
			wantStatus: http.StatusUnprocessableEntity, wantCode: "route_not_affected",
		},
		{
			name: "no local path", err: &detour.Error{Code: detour.ErrorNoDetourWithinSearchArea, Kind: detour.ErrorKindNoSolution, Message: "isolated"},
			wantStatus: http.StatusUnprocessableEntity, wantCode: "no_detour_within_search_area",
		},
		{
			name: "traffic unavailable", err: &detour.Error{Code: detour.ErrorTrafficUnavailable, Kind: detour.ErrorKindDependency, Message: "unavailable"},
			wantStatus: http.StatusServiceUnavailable, wantCode: "traffic_unavailable",
		},
		{
			name: "traffic coverage", err: &detour.Error{Code: detour.ErrorTrafficCoverageInsufficient, Kind: detour.ErrorKindDependency, Message: "coverage"},
			wantStatus: http.StatusServiceUnavailable, wantCode: "traffic_coverage_insufficient",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			planner := &fakeDetourPlanner{err: testCase.err}
			router := newTestRouterWithDetours(
				&fakeSimulator{}, &fakeComparator{}, planner, &fakeLines{},
			)
			response := doDetourRequest(t, router, detourJSON())
			assertErrorResponse(t, response, testCase.wantStatus, testCase.wantCode, testCase.wantField)
		})
	}
}

func TestCreateDetourMapsTimeoutAndHidesInternalErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "timeout", err: fmt.Errorf("route graph: %w", context.DeadlineExceeded), wantStatus: http.StatusGatewayTimeout, wantCode: "timeout"},
		{name: "internal", err: errors.New("postgresql://secret@private:5432"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			planner := &fakeDetourPlanner{err: testCase.err}
			router := newTestRouterWithDetours(
				&fakeSimulator{}, &fakeComparator{}, planner, &fakeLines{},
			)
			response := doDetourRequest(t, router, detourJSON())
			assertErrorResponse(t, response, testCase.wantStatus, testCase.wantCode, "")
			if bytes.Contains(response.Body.Bytes(), []byte("private")) {
				t.Fatalf("response leaked internal error: %s", response.Body.String())
			}
		})
	}
}

func doDetourRequest(
	t *testing.T,
	router http.Handler,
	body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/detours", bytes.NewReader(body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func detourJSON() []byte {
	return []byte(fmt.Sprintf(`{
		"route": %s,
		"cut": {
			"type": "LineString",
			"coordinates": [[-58.3804,-34.6004],[-58.3806,-34.6006]]
		},
		"criterion": "MENOR_TIEMPO"
	}`, validRouteJSON()))
}

func validRouteValue(t *testing.T) route.Route {
	t.Helper()
	var result route.Route
	if err := json.Unmarshal(validRouteJSON(), &result); err != nil {
		t.Fatalf("decode valid route: %v", err)
	}
	return result
}
