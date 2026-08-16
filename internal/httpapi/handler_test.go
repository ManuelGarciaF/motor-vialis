package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/httpapi"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
)

func TestHealth(t *testing.T) {
	router := newTestRouter(&fakeSimulator{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	if strings.TrimSpace(response.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("body = %s, want an ok response", response.Body.String())
	}
}

type fakeSimulator struct {
	result   simulation.Result
	err      error
	received simulation.Route
}

func (simulator *fakeSimulator) Simulate(
	_ context.Context,
	input simulation.Route,
) (simulation.Result, error) {
	simulator.received = input
	return simulator.result, simulator.err
}

type fakeComparator struct {
	result   simulation.Comparison
	err      error
	received simulation.ComparisonInput
	calls    int
}

func (comparator *fakeComparator) Compare(
	_ context.Context,
	input simulation.ComparisonInput,
) (simulation.Comparison, error) {
	comparator.calls++
	comparator.received = input
	return comparator.result, comparator.err
}

func newTestRouter(simulator httpapi.Simulator) http.Handler {
	return newTestRouterWith(simulator, &fakeComparator{})
}

func newTestRouterWith(
	simulator httpapi.Simulator,
	comparator httpapi.Comparator,
) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpapi.NewHandler(logger, simulator, comparator, 5*time.Second).Routes()
}
