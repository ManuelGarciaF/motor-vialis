package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
)

// Simulator runs a full simulation for one route. It is the same interface a
// future message-queue worker would call: neither the HTTP handler nor the
// worker owns simulation logic, they only adapt a transport to this call.
type Simulator interface {
	Simulate(ctx context.Context, input simulation.Route) (simulation.Result, error)
}

// Handler exposes the service's endpoints over HTTP.
type Handler struct {
	logger    *slog.Logger
	simulator Simulator
}

func NewHandler(logger *slog.Logger, simulator Simulator) *Handler {
	return &Handler{logger: logger, simulator: simulator}
}

func (handler *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.health)
	mux.HandleFunc("POST /simulations", handler.createSimulation)
	return handler.recoverPanic(handler.logRequest(mux))
}

func (handler *Handler) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, healthResponse{Status: "ok"})
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
				writeJSON(writer, http.StatusInternalServerError, healthResponse{Status: "error"})
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

type healthResponse struct {
	Status string `json:"status"`
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		slog.Error("encode HTTP response", "error", err)
	}
}
