package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
)

// listCombinationRanking answers which pairs of lines people appear to be
// combining, largest estimate first.
//
// It is a GET, unlike the corridor search: the whole question is three
// numbers, so it fits in a query string, and the answer is the same for every
// caller and worth caching.
func (handler *Handler) listCombinationRanking(
	writer http.ResponseWriter,
	request *http.Request,
) {
	values := request.URL.Query()

	hour, fieldErr := parseHour(values.Get("hour"))
	if fieldErr != nil {
		writeFieldError(writer, fieldErr)
		return
	}

	limit, fieldErr := parseCount(values.Get("limit"), "limit")
	if fieldErr != nil {
		writeFieldError(writer, fieldErr)
		return
	}

	offset, fieldErr := parseCount(values.Get("offset"), "offset")
	if fieldErr != nil {
		writeFieldError(writer, fieldErr)
		return
	}

	ctx, cancel := handler.withTimeout(request)
	defer cancel()

	page, err := handler.transfers.FindRanking(ctx, combinaciones.Request{
		Hour:   hour,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		handler.writeTransferError(writer, request, "list combination ranking", err)
		return
	}

	writeJSON(writer, http.StatusOK, page)
}

func writeFieldError(writer http.ResponseWriter, fieldErr *fieldError) {
	writeError(
		writer,
		http.StatusBadRequest,
		errorCodeValidation,
		fieldErr.Field,
		fieldErr.Message,
	)
}

// parseHour reads the optional banda horaria.
//
// Absent means the whole day, which is the ranking's default view and a real
// answer rather than a fallback: the question "which combinations does this
// city force" is not about any one hour. A present but unusable value is
// refused rather than silently widened to the day, because a client computing
// an hour wrongly would otherwise get a plausible answer to the wrong
// question.
func parseHour(raw string) (*int, *fieldError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 || parsed >= combinaciones.HoursInDay {
		return nil, &fieldError{
			Field:   "hour",
			Message: "must be an integer between 0 and 23",
		}
	}
	return &parsed, nil
}

// writeTransferError maps an error from the combinations service to an HTTP
// response.
//
// An out-of-range hour is reported as a field error even though the handler
// already rejects one: the service guards its own contract, and a guard that
// surfaced as a 500 would report our bug as an outage.
func (handler *Handler) writeTransferError(
	writer http.ResponseWriter,
	request *http.Request,
	operation string,
	err error,
) {
	if errors.Is(err, combinaciones.ErrHourOutOfRange) {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			"hour",
			"must be an integer between 0 and 23",
		)
		return
	}

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
			"the request took longer than the server allows",
		)
		return
	}

	handler.logger.Error(operation, "error", err)
	writeError(writer, http.StatusInternalServerError, errorCodeInternal, "", "internal error")
}
