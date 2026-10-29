package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
)

// maximumRequestBytes leaves ample room for route calculations without allowing unbounded bodies.
const maximumRequestBytes = 4 << 20

// Simulator runs a full simulation for one route.
type Simulator interface {
	Simulate(ctx context.Context, input simulation.Route) (simulation.Result, error)
}

// Comparator evaluates a proposed route against an existing one.
type Comparator interface {
	Compare(
		ctx context.Context,
		input simulation.ComparisonInput,
	) (simulation.Comparison, error)
	CompareDetour(
		ctx context.Context,
		input simulation.ComparisonInput,
	) (simulation.Comparison, error)
}

// DetourPlanner builds and evaluates a temporary route around one road cut.
type DetourPlanner interface {
	Plan(ctx context.Context, input detour.Input) (detour.Result, error)
}

// SegmentRouter routes one route segment through waypoints around an
// optional cut.
type SegmentRouter interface {
	Route(ctx context.Context, input detour.SegmentInput) (detour.SegmentRoute, error)
}

// Lines reads stored GTFS lines that can serve as proposal baselines.
type Lines interface {
	List(ctx context.Context, query lines.Query) (lines.Page, error)
	Get(ctx context.Context, id int64) (lines.Detail, error)
	FindSimilar(
		ctx context.Context,
		request lines.SimilarityRequest,
	) (lines.Similarities, error)
}

// Transfers ranks the pairs of lines people appear to be combining, so a
// client can see where the network forces a change of bus.
type Transfers interface {
	FindRanking(
		ctx context.Context,
		request combinaciones.Request,
	) (combinaciones.Page, error)
}

type Bottlenecks interface {
	List(context.Context, bottlenecks.DetectionQuery) (bottlenecks.DetectionPage, error)
}

type Bunching interface {
	Get(context.Context, int64) (bunching.Current, error)
	List(context.Context, bunching.HistoryQuery) (bunching.EventPage, error)
	Recurrence(context.Context) (bunching.RecurrencePage, error)
}

// Handler exposes the service's endpoints over HTTP.
type Handler struct {
	logger      *slog.Logger
	simulator   Simulator
	comparator  Comparator
	detours     DetourPlanner
	segments    SegmentRouter
	lines       Lines
	transfers   Transfers
	bunching    Bunching
	bottlenecks Bottlenecks
	timeout     time.Duration
	demoAt      time.Time
}

func NewHandler(
	logger *slog.Logger,
	simulator Simulator,
	comparator Comparator,
	detours DetourPlanner,
	storedLines Lines,
	transfers Transfers,
	timeout time.Duration,
	bunchingServices ...Bunching,
) *Handler {
	var bunchingService Bunching
	if len(bunchingServices) > 0 {
		bunchingService = bunchingServices[0]
	}
	return &Handler{
		bunching:   bunchingService,
		logger:     logger,
		simulator:  simulator,
		comparator: comparator,
		detours:    detours,
		lines:      storedLines,
		transfers:  transfers,
		timeout:    timeout,
	}
}

// WithSegmentRouter configures the optional segment router before Routes is
// served.
func (handler *Handler) WithSegmentRouter(service SegmentRouter) *Handler {
	handler.segments = service
	return handler
}

// WithBottlenecks configures the optional reader before Routes is served.
func (handler *Handler) WithBottlenecks(service Bottlenecks) *Handler {
	handler.bottlenecks = service
	return handler
}

// WithDemoTime enables historical defaults for worker APIs before serving.
// Explicit timestamps remain historical timestamps and are never rewritten.
func (handler *Handler) WithDemoTime(at time.Time) *Handler {
	handler.demoAt = at.UTC()
	return handler
}

func (handler *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /lines", handler.listLines)
	mux.HandleFunc("GET /bottlenecks", handler.listBottlenecks)
	mux.HandleFunc("GET /bunching", handler.listBunching)
	mux.HandleFunc("GET /bunching/recurrence", handler.getBunchingRecurrence)
	// "POST /lines/similar" and "GET /lines/{id}" do not compete: a pattern
	// with a method only matches that method, so no POST ever reaches the
	// detail lookup and "similar" can never arrive there as an id. A GET of
	// /lines/similar does land on the detail lookup, which answers that "id"
	// must be a positive integer — the honest reply to a search asked with the
	// wrong verb. Registering the literal path before the wildcard would not
	// change any of this; ServeMux prefers the more specific pattern whatever
	// the order.
	mux.HandleFunc("GET /lines/{id}", handler.getLine)
	mux.HandleFunc("GET /lines/{id}/bunching", handler.getBunching)
	mux.HandleFunc("POST /lines/similar", handler.findSimilarLines)
	mux.HandleFunc("GET /transfers", handler.listCombinationRanking)
	mux.HandleFunc("POST /simulations", handler.createSimulation)
	mux.HandleFunc("POST /comparisons", handler.createComparison)
	mux.HandleFunc("POST /comparisons/detour", handler.createDetourComparison)
	mux.HandleFunc("POST /detours", handler.createDetour)
	mux.HandleFunc("POST /routes/segment", handler.createSegmentRoute)
	var routes http.Handler = mux
	if !handler.demoAt.IsZero() {
		routes = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Vialis-Demo-Time", handler.demoAt.Format(time.RFC3339Nano))
			mux.ServeHTTP(w, r)
		})
	}
	return handler.recoverPanic(handler.logRequest(routes))
}

// withTimeout bounds backend work and preserves client cancellation.
func (handler *Handler) withTimeout(
	request *http.Request,
) (context.Context, context.CancelFunc) {
	if handler.timeout <= 0 {
		return context.WithCancel(request.Context())
	}
	return context.WithTimeout(request.Context(), handler.timeout)
}

func decodeBody(request *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, maximumRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("body must contain exactly one JSON value")
		}
		return err
	}
	return nil
}

func (handler *Handler) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		next.ServeHTTP(writer, request)
		handler.logger.Info("HTTP request",
			"method", request.Method,
			"path", request.URL.Path,
			"duration", time.Since(startedAt),
		)
	})
}

func (handler *Handler) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				handler.logger.Error("panic recovered", "error", recovered)
				writeError(
					writer,
					http.StatusInternalServerError,
					errorCodeInternal,
					"",
					"internal error",
				)
			}
		}()

		next.ServeHTTP(writer, request)
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
	}
}
