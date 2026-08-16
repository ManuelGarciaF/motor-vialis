package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestCreateSimulationReturnsResult(t *testing.T) {
	simulator := &fakeSimulator{}
	simulator.result.Global.Metrics.TotalDistanceMeters = 1500
	router := newTestRouter(simulator)

	response := doSimulationRequest(t, router, validRouteJSON())

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var got simulation.Result
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Global.Metrics.TotalDistanceMeters != 1500 {
		t.Fatalf("totalDistanceMeters = %v, want 1500", got.Global.Metrics.TotalDistanceMeters)
	}
	if simulator.received.Jurisdiction != route.JurisdictionCABA {
		t.Fatalf("simulator received jurisdiction = %q, want %q", simulator.received.Jurisdiction, route.JurisdictionCABA)
	}
}

func TestCreateSimulationRejectsMalformedJSON(t *testing.T) {
	router := newTestRouter(&fakeSimulator{})

	response := doSimulationRequest(t, router, []byte(`{"jurisdiction": "caba", "stops": [`))

	assertErrorResponse(t, response, http.StatusBadRequest, "validation_error", "")
}

func TestCreateSimulationRejectsUnknownFields(t *testing.T) {
	router := newTestRouter(&fakeSimulator{})

	response := doSimulationRequest(t, router, []byte(`{"jurisdiction": "caba", "stops": [], "unexpected": true}`))

	assertErrorResponse(t, response, http.StatusBadRequest, "validation_error", "")
}

func TestCreateSimulationMapsValidationError(t *testing.T) {
	simulator := &fakeSimulator{err: &route.ValidationError{
		Field:   "route.jurisdiction",
		Message: "must be caba, province, or national",
	}}
	router := newTestRouter(simulator)

	response := doSimulationRequest(t, router, validRouteJSON())

	assertErrorResponse(t, response, http.StatusBadRequest, "validation_error", "route.jurisdiction")
}

func TestCreateSimulationMapsInfrastructureError(t *testing.T) {
	simulator := &fakeSimulator{err: errors.New("estimate travel time: database unavailable")}
	router := newTestRouter(simulator)

	response := doSimulationRequest(t, router, validRouteJSON())

	assertErrorResponse(t, response, http.StatusInternalServerError, "internal_error", "")
	if bytes.Contains(response.Body.Bytes(), []byte("database unavailable")) {
		t.Fatalf("internal error body leaked the underlying error: %s", response.Body.String())
	}
}

func doSimulationRequest(t *testing.T, router http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/simulations", bytes.NewReader(body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertErrorResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
	wantField string,
) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, wantStatus, response.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, wantCode)
	}
	if body.Error.Field != wantField {
		t.Fatalf("error.field = %q, want %q", body.Error.Field, wantField)
	}
	if body.Error.Message == "" {
		t.Fatal("error.message = \"\", want a message")
	}
}

func validRouteJSON() []byte {
	return []byte(`{
		"jurisdiction": "caba",
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
