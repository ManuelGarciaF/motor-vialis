package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func newLinesRouter(stored *fakeLines) http.Handler {
	return newTestRouterWithAll(&fakeSimulator{}, &fakeComparator{}, stored)
}

func get(t *testing.T, router http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

func TestListLinesReturnsThePage(t *testing.T) {
	stored := &fakeLines{page: lines.Page{
		Lines: []lines.Summary{{
			ID:         12,
			Line:       "132",
			PublicName: "132 - Retiro",
			StopCount:  48,
		}},
		Total:  1,
		Limit:  50,
		Offset: 0,
	}}

	response := get(t, newLinesRouter(stored), "/lines")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}

	var body lines.Page
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Total != 1 || len(body.Lines) != 1 || body.Lines[0].ID != 12 {
		t.Fatalf("body = %#v, want the single stored line", body)
	}
}

func TestListLinesForwardsTheFilters(t *testing.T) {
	stored := &fakeLines{}

	response := get(
		t,
		newLinesRouter(stored),
		"/lines?search=132&limit=25&offset=50&bbox=-58.6,-34.8,-58.2,-34.4",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	if stored.received.Search != "132" {
		t.Fatalf("search = %q, want 132", stored.received.Search)
	}
	if stored.received.Limit != 25 || stored.received.Offset != 50 {
		t.Fatalf(
			"limit/offset = %d/%d, want 25/50",
			stored.received.Limit,
			stored.received.Offset,
		)
	}
	want := lines.Bounds{
		MinLongitude: -58.6,
		MinLatitude:  -34.8,
		MaxLongitude: -58.2,
		MaxLatitude:  -34.4,
	}
	if stored.received.Bounds == nil || *stored.received.Bounds != want {
		t.Fatalf("bounds = %#v, want %#v", stored.received.Bounds, want)
	}
}

func TestListLinesRejectsBadParameters(t *testing.T) {
	cases := []struct {
		name  string
		query string
		field string
	}{
		{"negative limit", "/lines?limit=-1", "limit"},
		{"non numeric offset", "/lines?offset=soon", "offset"},
		{"short bbox", "/lines?bbox=-58.6,-34.8,-58.2", "bbox"},
		{"non numeric bbox", "/lines?bbox=-58.6,-34.8,-58.2,north", "bbox"},
		{"out of range bbox", "/lines?bbox=-200,-34.8,-58.2,-34.4", "bbox"},
		{"inverted bbox", "/lines?bbox=-58.2,-34.8,-58.6,-34.4", "bbox"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			response := get(t, newLinesRouter(&fakeLines{}), testCase.query)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if field := errorField(t, response); field != testCase.field {
				t.Fatalf("field = %q, want %q", field, testCase.field)
			}
		})
	}
}

func TestGetLineReturnsARouteAndItsStops(t *testing.T) {
	stored := &fakeLines{detail: lines.Detail{
		Line: lines.Summary{ID: 12, Line: "132"},
		Route: route.Route{Stops: []route.Stop{
			{ID: "2031665", Position: route.Position{Latitude: -34.6, Longitude: -58.38}},
		}},
		Stops: []lines.StopDescription{{StopOrder: 0, StopID: "2031665", Name: "Retiro"}},
	}}

	response := get(t, newLinesRouter(stored), "/lines/12")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}
	if stored.requestedID != 12 {
		t.Fatalf("requested id = %d, want 12", stored.requestedID)
	}
	// The exported route claims no tariff authority: GTFS records none, and the
	// caller picks one when it simulates.
	if strings.Contains(response.Body.String(), "jurisdiction") {
		t.Fatalf("body = %s, want no jurisdiction field", response.Body)
	}
}

func TestGetLineRejectsANonNumericID(t *testing.T) {
	response := get(t, newLinesRouter(&fakeLines{}), "/lines/doce")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if field := errorField(t, response); field != "id" {
		t.Fatalf("field = %q, want id", field)
	}
}

func TestGetLineReportsAnUnknownID(t *testing.T) {
	response := get(t, newLinesRouter(&fakeLines{err: lines.ErrNotFound}), "/lines/999")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if code := errorCode(t, response); code != "not_found" {
		t.Fatalf("code = %q, want not_found", code)
	}
}

// A stored line the engine cannot express as a valid route is neither the
// caller's mistake nor something a retry fixes, so it gets its own status.
func TestGetLineReportsALineItCannotSimulate(t *testing.T) {
	stored := &fakeLines{err: &lines.NotSimulableError{
		LineID: 12,
		Reason: "no stored geometry between stops[3] and stops[4]",
	}}

	response := get(t, newLinesRouter(stored), "/lines/12")
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
	if code := errorCode(t, response); code != "line_not_simulable" {
		t.Fatalf("code = %q, want line_not_simulable", code)
	}
}

func decodeError(t *testing.T, response *httptest.ResponseRecorder) struct {
	Error struct {
		Code    string `json:"code"`
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"error"`
} {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body
}

func errorField(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	return decodeError(t, response).Error.Field
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	return decodeError(t, response).Error.Code
}
