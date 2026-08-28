package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
)

// maximumRequestBytes bounds a request body. A route of 140 stops exported
// from stored GTFS geometry is around 45 KB, so this leaves ample room for two
// of them while refusing anything that could only be an attempt to exhaust
// memory.
const maximumRequestBytes = 4 << 20

// Simulator runs a full simulation for one route. It is the same interface a
// future message-queue worker would call: neither the HTTP handler nor the
// worker owns simulation logic, they only adapt a transport to this call.
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

// Lines reads the stored GTFS lines the engine already knows, so a client can
// pick one as the starting point of a proposal.
type Lines interface {
	List(ctx context.Context, query lines.Query) (lines.Page, error)
	Get(ctx context.Context, id int64) (lines.Detail, error)
}

// Handler exposes the service's endpoints over HTTP.
type Handler struct {
	logger     *slog.Logger
	simulator  Simulator
	comparator Comparator
	lines      Lines
	timeout    time.Duration
}

func NewHandler(
	logger *slog.Logger,
	simulator Simulator,
	comparator Comparator,
	storedLines Lines,
	timeout time.Duration,
) *Handler {
	return &Handler{
		logger:     logger,
		simulator:  simulator,
		comparator: comparator,
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
	return handler.recoverPanic(handler.logRequest(mux))
}

// withTimeout bounds the work a single request may start.
//
// The server's write timeout closes the connection but leaves the handler
// running, so without this an abandoned request would keep querying the
// database for as long as it liked. Deriving the deadline from the request
// context also preserves cancellation when the client hangs up.
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
	return decoder.Decode(target)
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
