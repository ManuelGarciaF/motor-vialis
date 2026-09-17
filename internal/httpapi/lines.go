package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
)

func (handler *Handler) listLines(writer http.ResponseWriter, request *http.Request) {
	query, fieldErr := parseLineQuery(request)
	if fieldErr != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			fieldErr.Field,
			fieldErr.Message,
		)
		return
	}

	ctx, cancel := handler.withTimeout(request)
	defer cancel()

	page, err := handler.lines.List(ctx, query)
	if err != nil {
		handler.writeLineError(writer, request, "list stored lines", err)
		return
	}

	writeJSON(writer, http.StatusOK, page)
}

func (handler *Handler) getLine(writer http.ResponseWriter, request *http.Request) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			"id",
			"must be a positive integer",
		)
		return
	}

	ctx, cancel := handler.withTimeout(request)
	defer cancel()

	detail, err := handler.lines.Get(ctx, id)
	if err != nil {
		handler.writeLineError(writer, request, "read stored line", err)
		return
	}

	writeJSON(writer, http.StatusOK, detail)
}

// fieldError names the request field a caller has to fix.
type fieldError struct {
	Field   string
	Message string
}

func parseLineQuery(request *http.Request) (lines.Query, *fieldError) {
	values := request.URL.Query()
	query := lines.Query{Search: values.Get("search")}

	limit, fieldErr := parseCount(values.Get("limit"), "limit")
	if fieldErr != nil {
		return lines.Query{}, fieldErr
	}
	query.Limit = limit

	offset, fieldErr := parseCount(values.Get("offset"), "offset")
	if fieldErr != nil {
		return lines.Query{}, fieldErr
	}
	query.Offset = offset

	bounds, fieldErr := parseBounds(values.Get("bbox"))
	if fieldErr != nil {
		return lines.Query{}, fieldErr
	}
	query.Bounds = bounds

	return query, nil
}

func parseCount(raw, name string) (int, *fieldError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return 0, &fieldError{Field: name, Message: "must be a non-negative integer"}
	}
	return parsed, nil
}

// parseBounds reads the "minLon,minLat,maxLon,maxLat" viewport a map sends.
func parseBounds(raw string) (*lines.Bounds, *fieldError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return nil, &fieldError{
			Field:   "bbox",
			Message: "must be minLongitude,minLatitude,maxLongitude,maxLatitude",
		}
	}
	corners := make([]float64, 4)
	for index, part := range parts {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, &fieldError{
				Field:   "bbox",
				Message: fmt.Sprintf("value %d is not a number: %q", index, part),
			}
		}
		corners[index] = parsed
	}

	bounds := lines.Bounds{
		MinLongitude: corners[0],
		MinLatitude:  corners[1],
		MaxLongitude: corners[2],
		MaxLatitude:  corners[3],
	}
	if bounds.MinLongitude < -180 || bounds.MaxLongitude > 180 ||
		bounds.MinLatitude < -90 || bounds.MaxLatitude > 90 {
		return nil, &fieldError{
			Field:   "bbox",
			Message: "must be within -180..180 longitude and -90..90 latitude",
		}
	}
	// An inverted rectangle would silently match nothing, which reads as "there
	// are no lines here" rather than as the mistake it is.
	if bounds.MinLongitude > bounds.MaxLongitude ||
		bounds.MinLatitude > bounds.MaxLatitude {
		return nil, &fieldError{
			Field:   "bbox",
			Message: "minimum corner must not be greater than the maximum corner",
		}
	}
	return &bounds, nil
}

// writeLineError distinguishes missing lines from invalid stored geometry.
func (handler *Handler) writeLineError(
	writer http.ResponseWriter,
	request *http.Request,
	operation string,
	err error,
) {
	if errors.Is(err, lines.ErrNotFound) {
		writeError(
			writer,
			http.StatusNotFound,
			errorCodeNotFound,
			"id",
			"no stored line has this id",
		)
		return
	}

	var notSimulable *lines.NotSimulableError
	if errors.As(err, &notSimulable) {
		handler.logger.Warn(operation, "error", err)
		writeError(
			writer,
			http.StatusUnprocessableEntity,
			errorCodeNotSimulable,
			"",
			notSimulable.Error(),
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
