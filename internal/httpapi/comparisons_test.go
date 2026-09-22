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

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestCreateComparisonReturnsBothResultsAndTheDelta(t *testing.T) {
	comparator := &fakeComparator{}
	comparator.result.Delta.Demand.PotentialDemand.Absolute = 250
	comparator.result.Proposed.Global.Demand.PotentialDemand = 1250
	router := newTestRouterWith(&fakeSimulator{}, comparator)

	response := doComparisonRequest(t, router, comparisonJSON())

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var got simulation.Comparison
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Delta.Demand.PotentialDemand.Absolute != 250 {
		t.Fatalf("delta = %#v", got.Delta.Demand.PotentialDemand)
	}
	if got.Proposed.Global.Demand.PotentialDemand != 1250 {
		t.Fatalf("proposed = %#v", got.Proposed.Global)
	}
	if comparator.received.Baseline.Jurisdiction != route.JurisdictionCABA ||
		len(comparator.received.Proposed.Stops) != 2 {
		t.Fatalf("comparator received = %#v", comparator.received)
	}
}

func TestCreateComparisonRequiresBothRoutes(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantField string
	}{
		{
			name:      "missing baseline",
			body:      fmt.Sprintf(`{"proposed": %s}`, validRouteJSON()),
			wantField: "baseline",
		},
		{
			name:      "missing proposed",
			body:      fmt.Sprintf(`{"baseline": %s}`, validRouteJSON()),
			wantField: "proposed",
		},
		{
			name:      "missing both",
			body:      `{}`,
			wantField: "baseline",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			comparator := &fakeComparator{}
			router := newTestRouterWith(&fakeSimulator{}, comparator)

			response := doComparisonRequest(t, router, []byte(testCase.body))

			assertErrorResponse(
				t,
				response,
				http.StatusBadRequest,
				"validation_error",
				testCase.wantField,
			)
			if comparator.calls != 0 {
				t.Fatalf("comparator ran %d times for an incomplete request", comparator.calls)
			}
		})
	}
}

func TestCreateComparisonRejectsUnknownFields(t *testing.T) {
	router := newTestRouterWith(&fakeSimulator{}, &fakeComparator{})

	body := fmt.Sprintf(
		`{"baseline": %s, "proposed": %s, "unexpected": true}`,
		validRouteJSON(),
		validRouteJSON(),
	)
	response := doComparisonRequest(t, router, []byte(body))

	assertErrorResponse(t, response, http.StatusBadRequest, "validation_error", "")
}

// Unknown-field rejection applies recursively to nested routes.
func TestCreateComparisonRejectsUnknownFieldsInsideARoute(t *testing.T) {
	router := newTestRouterWith(&fakeSimulator{}, &fakeComparator{})

	body := fmt.Sprintf(
		`{"baseline": {"jurisdiction": "caba", "stops": [{"id": "A", "name": "x"}]},
		  "proposed": %s}`,
		validRouteJSON(),
	)
	response := doComparisonRequest(t, router, []byte(body))

	assertErrorResponse(t, response, http.StatusBadRequest, "validation_error", "")
}

func TestCreateComparisonReportsWhichRouteIsInvalid(t *testing.T) {
	comparator := &fakeComparator{err: &route.ValidationError{
		Field:   "proposed.stops[1].pathToNext",
		Message: "must be omitted on the last stop",
	}}
	router := newTestRouterWith(&fakeSimulator{}, comparator)

	response := doComparisonRequest(t, router, comparisonJSON())

	assertErrorResponse(
		t,
		response,
		http.StatusBadRequest,
		"validation_error",
		"proposed.stops[1].pathToNext",
	)
}

func TestCreateComparisonReportsATimeout(t *testing.T) {
	comparator := &fakeComparator{err: fmt.Errorf("simulate baseline: %w", context.DeadlineExceeded)}
	router := newTestRouterWith(&fakeSimulator{}, comparator)

	response := doComparisonRequest(t, router, comparisonJSON())

	assertErrorResponse(t, response, http.StatusGatewayTimeout, "timeout", "")
}

func TestCreateComparisonHidesInternalErrors(t *testing.T) {
	comparator := &fakeComparator{err: errors.New("connection refused to 10.0.0.5:5432")}
	router := newTestRouterWith(&fakeSimulator{}, comparator)

	response := doComparisonRequest(t, router, comparisonJSON())

	assertErrorResponse(t, response, http.StatusInternalServerError, "internal_error", "")
	if bytes.Contains(response.Body.Bytes(), []byte("10.0.0.5")) {
		t.Fatalf("response leaked internal detail: %s", response.Body.String())
	}
}

func doComparisonRequest(
	t *testing.T,
	router http.Handler,
	body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/comparisons", bytes.NewReader(body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func comparisonJSON() []byte {
	return []byte(fmt.Sprintf(
		`{"baseline": %s, "proposed": %s}`,
		validRouteJSON(),
		validRouteJSON(),
	))
}
