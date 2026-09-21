package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

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
}

// DetourPlanner builds and evaluates a temporary route around one road cut.
type DetourPlanner interface {
	Plan(ctx context.Context, input detour.Input) (detour.Result, error)
}

// Lines reads stored GTFS lines that can serve as proposal baselines.
type Lines interface {
	List(ctx context.Context, query lines.Query) (lines.Page, error)
	Get(ctx context.Context, id int64) (lines.Detail, error)
}

// Handler exposes the service's endpoints over HTTP.
type Handler struct {
	logger     *slog.Logger
	simulator  Simulator
	comparator Comparator
	detours    DetourPlanner
	lines      Lines
	timeout    time.Duration
}

func NewHandler(
	logger *slog.Logger,
	simulator Simulator,
	comparator Comparator,
	detours DetourPlanner,
	storedLines Lines,
	timeout time.Duration,
) *Handler {
	return &Handler{
		logger:     logger,
		simulator:  simulator,
		comparator: comparator,
		detours:    detours,
		lines:      storedLines,
		timeout:    timeout,
	}
}

func (handler *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /lines", handler.listLines)
	mux.HandleFunc("GET /lines/{id}", handler.getLine)
	mux.HandleFunc("POST /simulations", handler.createSimulation)
	mux.HandleFunc("POST /comparisons", handler.createComparison)
	mux.HandleFunc("POST /detours", handler.createDetour)
	return handler.recoverPanic(handler.logRequest(mux))
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
