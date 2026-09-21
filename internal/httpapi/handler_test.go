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
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
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

type fakeDetourPlanner struct {
	result   detour.Result
	err      error
	received detour.Input
	calls    int
}

func (planner *fakeDetourPlanner) Plan(
	_ context.Context,
	input detour.Input,
) (detour.Result, error) {
	planner.calls++
	planner.received = input
	return planner.result, planner.err
}

type fakeLines struct {
	page        lines.Page
	detail      lines.Detail
	err         error
	received    lines.Query
	requestedID int64
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
	return newTestRouterWithDetours(simulator, comparator, &fakeDetourPlanner{}, storedLines)
}

func newTestRouterWithDetours(
	simulator httpapi.Simulator,
	comparator httpapi.Comparator,
	detours httpapi.DetourPlanner,
	storedLines httpapi.Lines,
) http.Handler {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return httpapi.NewHandler(
		logger,
		simulator,
		comparator,
		detours,
		storedLines,
		5*time.Second,
	).Routes()
}
