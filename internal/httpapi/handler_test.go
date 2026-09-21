package httpapi_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/httpapi"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
)

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

type fakeLines struct {
	page     lines.Page
	detail   lines.Detail
	similar  lines.Similarities
	err      error
	received lines.Query
	// requestedID records the id of the last Get call.
	requestedID int64
	// receivedSimilarity records the last corridor search.
	receivedSimilarity lines.SimilarityRequest
}

func (stored *fakeLines) List(
	_ context.Context,
	query lines.Query,
) (lines.Page, error) {
	stored.received = query
	return stored.page, stored.err
}

func (stored *fakeLines) Get(_ context.Context, id int64) (lines.Detail, error) {
	stored.requestedID = id
	return stored.detail, stored.err
}

func (stored *fakeLines) FindSimilar(
	_ context.Context,
	request lines.SimilarityRequest,
) (lines.Similarities, error) {
	stored.receivedSimilarity = request
	return stored.similar, stored.err
}

func newTestRouter(simulator httpapi.Simulator) http.Handler {
	return newTestRouterWith(simulator, &fakeComparator{})
}

func newTestRouterWith(
	simulator httpapi.Simulator,
	comparator httpapi.Comparator,
) http.Handler {
	return newTestRouterWithAll(simulator, comparator, &fakeLines{})
}

func newTestRouterWithAll(
	simulator httpapi.Simulator,
	comparator httpapi.Comparator,
	storedLines httpapi.Lines,
) http.Handler {
	return newTestRouterWithTransfers(
		simulator,
		comparator,
		storedLines,
		&fakeTransfers{},
	)
}

func newTestRouterWithTransfers(
	simulator httpapi.Simulator,
	comparator httpapi.Comparator,
	storedLines httpapi.Lines,
	transfers httpapi.Transfers,
) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpapi.NewHandler(
		logger,
		simulator,
		comparator,
		storedLines,
		transfers,
		5*time.Second,
	).Routes()
}
