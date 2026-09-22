package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

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
	errorCodeNotFound   = "not_found"
	errorCodeTimeout    = "timeout"
	errorCodeInternal   = "internal_error"
	// A stored line may exist even when its geometry cannot be simulated.
	errorCodeNotSimulable = "line_not_simulable"
)

func writeError(writer http.ResponseWriter, status int, code, field, message string) {
	writeJSON(writer, status, errorResponse{Error: errorBody{
		Code:    code,
		Field:   field,
		Message: message,
	}})
}

// writeEstimationError preserves the distinction between invalid input and service failures.
func (handler *Handler) writeEstimationError(
	writer http.ResponseWriter,
	request *http.Request,
	operation string,
	err error,
) {
	var validationError *route.ValidationError
	if errors.As(err, &validationError) {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			validationError.Field,
			validationError.Message,
		)
		return
	}

	// There is no client left to receive a response after cancellation.
	if errors.Is(request.Context().Err(), context.Canceled) {
		handler.logger.Info("client cancelled request", "operation", operation)
		return
	}

	if errors.Is(err, context.DeadlineExceeded) {
		handler.logger.Warn(operation, "error", err)
		writeError(
			writer,
			http.StatusGatewayTimeout,
			errorCodeTimeout,
			"",
			"the simulation took longer than the server allows",
		)
		return
	}

	handler.logger.Error(operation, "error", err)
	writeError(writer, http.StatusInternalServerError, errorCodeInternal, "", "internal error")
}
