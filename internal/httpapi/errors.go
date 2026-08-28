package httpapi

import (
	"context"
	"errors"
	"net/http"

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
	errorCodeNotFound   = "not_found"
	errorCodeTimeout    = "timeout"
	errorCodeInternal   = "internal_error"
	// errorCodeNotSimulable marks a stored line the engine cannot express as a
	// valid route. It is neither the caller's mistake nor a transient failure,
	// so retrying or fixing the request will not help.
	errorCodeNotSimulable = "line_not_simulable"
)

func writeError(writer http.ResponseWriter, status int, code, field, message string) {
	writeJSON(writer, status, errorResponse{Error: errorBody{
		Code:    code,
		Field:   field,
		Message: message,
	}})
}

// writeEstimationError maps an error from the simulation service to an HTTP
// response, keeping the validation-versus-infrastructure distinction in one
// place. A future queue worker needs the same distinction to decide whether a
// message is worth retrying, so it should reuse this classification rather
// than reimplement it.
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

	// A client that hung up is not a failure of this service, and its response
	// has nowhere to go, so it is recorded but not answered.
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
