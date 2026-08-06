package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// errorResponse mirrors the {code, field?, message} shape documented in
// docs/openapi.yaml, reused as-is by the future queue message contract.
type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

const (
	errorCodeValidation = "validation_error"
	errorCodeInternal   = "internal_error"
)

func (handler *Handler) createSimulation(writer http.ResponseWriter, request *http.Request) {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()

	var input simulation.Route
	if err := decoder.Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, errorResponse{Error: errorBody{
			Code:    errorCodeValidation,
			Message: fmt.Sprintf("invalid JSON body: %v", err),
		}})
		return
	}

	result, err := handler.simulator.Simulate(request.Context(), input)
	if err != nil {
		handler.writeSimulateError(writer, err)
		return
	}

	writeJSON(writer, http.StatusOK, result)
}

// writeSimulateError maps a Simulate() error to an HTTP response, keeping
// the validation-vs-infrastructure distinction in one place. A future queue
// worker needs the exact same distinction (retryable or not), so it should
// reuse this classification rather than reimplement it.
func (handler *Handler) writeSimulateError(writer http.ResponseWriter, err error) {
	var validationErr *route.ValidationError
	if errors.As(err, &validationErr) {
		writeJSON(writer, http.StatusBadRequest, errorResponse{Error: errorBody{
			Code:    errorCodeValidation,
			Field:   validationErr.Field,
			Message: validationErr.Message,
		}})
		return
	}

	handler.logger.Error("simulate route", "error", err)
	writeJSON(writer, http.StatusInternalServerError, errorResponse{Error: errorBody{
		Code:    errorCodeInternal,
		Message: "internal error",
	}})
}
