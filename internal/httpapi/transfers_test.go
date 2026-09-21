package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
)

type fakeTransfers struct {
	received combinaciones.Request
	calls    int
	result   combinaciones.Page
	err      error
}

func (fake *fakeTransfers) FindRanking(
	_ context.Context,
	request combinaciones.Request,
) (combinaciones.Page, error) {
	fake.received = request
	fake.calls++
	return fake.result, fake.err
}

func getTransfers(t *testing.T, transfers *fakeTransfers, target string) *httptest.ResponseRecorder {
	t.Helper()
	router := newTestRouterWithTransfers(
		&fakeSimulator{},
		&fakeComparator{},
		&fakeLines{},
		transfers,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func TestListCombinationRankingPassesTheWindowAndHour(t *testing.T) {
	transfers := &fakeTransfers{}

	recorder := getTransfers(t, transfers, "/transfers?hour=9&limit=3&offset=20")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
	if transfers.received.Hour == nil || *transfers.received.Hour != 9 {
		t.Errorf("hour = %v, want 9", transfers.received.Hour)
	}
	if transfers.received.Limit != 3 {
		t.Errorf("limit = %d, want 3", transfers.received.Limit)
	}
	if transfers.received.Offset != 20 {
		t.Errorf("offset = %d, want 20", transfers.received.Offset)
	}
}

// An absent hour is the ranking's default view, not an error: the question
// "which combinations does this city force" is not about any one hour.
func TestListCombinationRankingDefaultsToTheWholeDay(t *testing.T) {
	transfers := &fakeTransfers{}

	recorder := getTransfers(t, transfers, "/transfers")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
	if transfers.calls != 1 {
		t.Fatalf("service called %d times, want 1", transfers.calls)
	}
	if transfers.received.Hour != nil {
		t.Errorf("hour = %v, want nil", *transfers.received.Hour)
	}
}

// An omitted limit must reach the service as zero, which is how it asks for
// the policy default. Substituting a number here would move that decision out
// of the policy and into the transport.
func TestListCombinationRankingLeavesAnOmittedLimitToThePolicy(t *testing.T) {
	transfers := &fakeTransfers{}

	recorder := getTransfers(t, transfers, "/transfers?hour=0")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
	if transfers.received.Limit != 0 {
		t.Errorf("limit = %d, want 0", transfers.received.Limit)
	}
}

// A present but unusable hour is refused rather than widened to the whole day:
// a client computing an hour wrongly would otherwise get a plausible answer to
// the wrong question.
func TestListCombinationRankingRejectsAnUnusableParameter(t *testing.T) {
	testCases := map[string]struct {
		target string
		field  string
	}{
		"hour not a number": {target: "/transfers?hour=nueve", field: "hour"},
		"negative hour":     {target: "/transfers?hour=-1", field: "hour"},
		"past midnight":     {target: "/transfers?hour=24", field: "hour"},
		"negative limit":    {target: "/transfers?hour=9&limit=-2", field: "limit"},
		"negative offset":   {target: "/transfers?hour=9&offset=-2", field: "offset"},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			transfers := &fakeTransfers{}

			recorder := getTransfers(t, transfers, testCase.target)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf(
					"status = %d, want %d: %s",
					recorder.Code, http.StatusBadRequest, recorder.Body,
				)
			}
			if transfers.calls != 0 {
				t.Errorf("service was called %d times, want 0", transfers.calls)
			}
			var body struct {
				Error struct {
					Code  string `json:"code"`
					Field string `json:"field"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Error.Code != "validation_error" {
				t.Errorf("code = %q, want %q", body.Error.Code, "validation_error")
			}
			if body.Error.Field != testCase.field {
				t.Errorf("field = %q, want %q", body.Error.Field, testCase.field)
			}
		})
	}
}

// `?hour=` is a form that submitted an empty field, not a client asking for
// hour zero, and parseCount treats an empty limit the same way. Refusing it
// would make an empty select box an error instead of the default view.
func TestListCombinationRankingTreatsAnEmptyHourAsTheWholeDay(t *testing.T) {
	transfers := &fakeTransfers{}

	recorder := getTransfers(t, transfers, "/transfers?hour=")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body)
	}
	if transfers.received.Hour != nil {
		t.Errorf("hour = %v, want nil", *transfers.received.Hour)
	}
}

// The service guards the same range the handler does. If that guard ever fires
// it is a bug of ours, but reporting it as a 500 would send the caller looking
// for an outage instead of at their request.
func TestListCombinationRankingReportsAnOutOfRangeHourAsAFieldError(t *testing.T) {
	transfers := &fakeTransfers{err: combinaciones.ErrHourOutOfRange}

	recorder := getTransfers(t, transfers, "/transfers?hour=9")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d: %s",
			recorder.Code, http.StatusBadRequest, recorder.Body,
		)
	}
}
