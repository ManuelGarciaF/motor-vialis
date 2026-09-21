package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func doSimilarLinesRequest(
	t *testing.T,
	router http.Handler,
	body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/lines/similar", bytes.NewReader(body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

// The drawn route carries no jurisdiction on purpose: the search is geometric
// and no tariff table is read.
func drawnRouteJSON() []byte {
	return []byte(`{
		"stops": [
			{
				"id": "A",
				"position": {"latitude": -34.6000, "longitude": -58.3800},
				"pathToNext": {
					"type": "LineString",
					"coordinates": [[-58.3800, -34.6000], [-58.3810, -34.6010]]
				}
			},
			{
				"id": "B",
				"position": {"latitude": -34.6010, "longitude": -58.3810}
			}
		]
	}`)
}

func similarLinesJSON() []byte {
	return []byte(fmt.Sprintf(`{"route": %s}`, drawnRouteJSON()))
}

func TestFindSimilarLinesReturnsTheRankedShortlist(t *testing.T) {
	minutes := 74
	stored := &fakeLines{similar: lines.Similarities{
		Limit: 10,
		Lines: []lines.Similarity{{
			Line:                      lines.Summary{ID: 12, Line: "132"},
			CoverageOfProposed:        0.91,
			CoverageOfStored:          0.34,
			OriginDistanceMeters:      120.5,
			DestinationDistanceMeters: 4200,
			Metrics: lines.StoredMetrics{
				DistanceMeters: 24000,
				TotalMinutes:   &minutes,
			},
		}},
	}}

	response := doSimilarLinesRequest(t, newLinesRouter(stored), similarLinesJSON())

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body lines.Similarities
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Lines) != 1 || body.Lines[0].Line.ID != 12 {
		t.Fatalf("body = %#v, want the single candidate", body)
	}
	// Both coverages survive the round trip: collapsing them into one score
	// would lose the "yours is a fragment of that line" reading this pair
	// carries.
	if body.Lines[0].CoverageOfProposed != 0.91 || body.Lines[0].CoverageOfStored != 0.34 {
		t.Fatalf("coverages = %#v", body.Lines[0])
	}
	if len(stored.receivedSimilarity.Route.Stops) != 2 {
		t.Fatalf("service received = %#v", stored.receivedSimilarity)
	}
}

// A measurement the pipeline never took must not reach the client as a zero,
// so the field is absent instead.
func TestFindSimilarLinesOmitsMetricsThatWereNeverMeasured(t *testing.T) {
	stored := &fakeLines{similar: lines.Similarities{
		Limit: 10,
		Lines: []lines.Similarity{{
			Line:    lines.Summary{ID: 12},
			Metrics: lines.StoredMetrics{DistanceMeters: 24000},
		}},
	}}

	response := doSimilarLinesRequest(t, newLinesRouter(stored), similarLinesJSON())

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, absent := range []string{"totalMinutes", "passengerFlow", "revenue"} {
		if bytes.Contains(response.Body.Bytes(), []byte(absent)) {
			t.Fatalf("body = %s, want no %q field", response.Body, absent)
		}
	}
}

func TestFindSimilarLinesRequiresTheRoute(t *testing.T) {
	response := doSimilarLinesRequest(t, newLinesRouter(&fakeLines{}), []byte(`{}`))

	assertErrorResponse(t, response, http.StatusBadRequest, "validation_error", "route")
}

// Zero and a negative number are a client computing a page size wrongly;
// answering with the default would hide the bug.
func TestFindSimilarLinesRejectsANonPositiveLimit(t *testing.T) {
	for _, limit := range []string{"0", "-5"} {
		t.Run(limit, func(t *testing.T) {
			body := fmt.Sprintf(`{"route": %s, "limit": %s}`, drawnRouteJSON(), limit)
			response := doSimilarLinesRequest(
				t,
				newLinesRouter(&fakeLines{}),
				[]byte(body),
			)

			assertErrorResponse(
				t,
				response,
				http.StatusBadRequest,
				"validation_error",
				"limit",
			)
		})
	}
}

func TestFindSimilarLinesForwardsTheLimit(t *testing.T) {
	stored := &fakeLines{}
	body := fmt.Sprintf(`{"route": %s, "limit": 5}`, drawnRouteJSON())

	response := doSimilarLinesRequest(t, newLinesRouter(stored), []byte(body))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if stored.receivedSimilarity.Limit != 5 {
		t.Fatalf("limit = %d, want 5", stored.receivedSimilarity.Limit)
	}
}

func TestFindSimilarLinesRejectsUnknownFields(t *testing.T) {
	body := fmt.Sprintf(`{"route": %s, "unexpected": true}`, drawnRouteJSON())

	response := doSimilarLinesRequest(t, newLinesRouter(&fakeLines{}), []byte(body))

	assertErrorResponse(t, response, http.StatusBadRequest, "validation_error", "")
}

// The field path is the one the caller sent, so the same error reads the same
// way here as it does on /simulations.
func TestFindSimilarLinesReportsAValidationError(t *testing.T) {
	stored := &fakeLines{err: &route.ValidationError{
		Field:   "route.stops",
		Message: "must contain at least two stops",
	}}

	response := doSimilarLinesRequest(t, newLinesRouter(stored), similarLinesJSON())

	assertErrorResponse(
		t,
		response,
		http.StatusBadRequest,
		"validation_error",
		"route.stops",
	)
}

func TestFindSimilarLinesReportsATimeout(t *testing.T) {
	stored := &fakeLines{err: fmt.Errorf(
		"find similar stored lines: %w",
		context.DeadlineExceeded,
	)}

	response := doSimilarLinesRequest(t, newLinesRouter(stored), similarLinesJSON())

	assertErrorResponse(t, response, http.StatusGatewayTimeout, "timeout", "")
}

// "similar" must never be read as a line id, and the detail lookup must never
// answer a POST. The two patterns differ by method, so neither can happen.
func TestSimilarPathDoesNotCollideWithTheDetailLookup(t *testing.T) {
	stored := &fakeLines{}
	router := newLinesRouter(stored)

	doSimilarLinesRequest(t, router, similarLinesJSON())
	if stored.requestedID != 0 {
		t.Fatalf("the detail lookup ran with id %d", stored.requestedID)
	}

	posted := httptest.NewRecorder()
	router.ServeHTTP(
		posted,
		httptest.NewRequest(http.MethodPost, "/lines/12", bytes.NewReader(nil)),
	)
	if posted.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /lines/12 status = %d, want %d",
			posted.Code, http.StatusMethodNotAllowed)
	}
	if stored.requestedID != 0 {
		t.Fatalf("the detail lookup ran for a POST, with id %d", stored.requestedID)
	}

	// A GET of the search path does reach the detail lookup, which reports the
	// only thing it can: "similar" is not an id.
	response := get(t, router, "/lines/similar")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("GET /lines/similar status = %d, want %d",
			response.Code, http.StatusBadRequest)
	}
	if field := errorField(t, response); field != "id" {
		t.Fatalf("field = %q, want id", field)
	}
}
