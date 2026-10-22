package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func (h *Handler) listBottlenecks(w http.ResponseWriter, r *http.Request) {
	q, err := parseBottlenecksQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errorCodeValidation, err.Field, err.Message)
		return
	}
	if h.bottlenecks == nil {
		writeError(w, http.StatusServiceUnavailable, "bottlenecks_unavailable", "", "bottlenecks is not configured")
		return
	}
	ctx, cancel := h.withTimeout(r)
	defer cancel()
	page, serviceErr := h.bottlenecks.List(ctx, q)
	if serviceErr != nil {
		var validation *bottlenecks.DetectionQueryError
		if errors.As(serviceErr, &validation) {
			writeError(w, http.StatusBadRequest, errorCodeValidation, validation.Field, validation.Message)
			return
		}
		h.writeLineError(w, r, "read bottlenecks", serviceErr)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func parseBottlenecksQuery(r *http.Request) (bottlenecks.DetectionQuery, *fieldError) {
	q := bottlenecks.DetectionQuery{}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return q, &fieldError{"query", "invalid query encoding"}
	}
	for key, entries := range values {
		if key != "from" && key != "to" && key != "line" && key != "bbox" {
			return q, &fieldError{key, "unknown query parameter"}
		}
		if len(entries) != 1 {
			return q, &fieldError{key, "must appear only once"}
		}
	}
	for _, field := range []string{"from", "to"} {
		at, err := time.Parse(time.RFC3339Nano, values.Get(field))
		if err != nil {
			return q, &fieldError{field, "must be an RFC3339 timestamp with timezone"}
		}
		if field == "from" {
			q.From = at
		} else {
			q.To = at
		}
	}
	q.Line = values.Get("line")
	if _, ok := values["bbox"]; ok && values.Get("bbox") == "" {
		return q, &fieldError{"bbox", "must not be empty"}
	}
	bounds, boundsErr := parseBounds(values.Get("bbox"))
	if boundsErr != nil {
		return q, boundsErr
	}
	if bounds != nil {
		q.Bounds = &bottlenecks.DetectionBounds{MinLongitude: bounds.MinLongitude, MinLatitude: bounds.MinLatitude, MaxLongitude: bounds.MaxLongitude, MaxLatitude: bounds.MaxLatitude}
	}
	if err := q.Validate(); err != nil {
		var validation *bottlenecks.DetectionQueryError
		if errors.As(err, &validation) {
			return q, &fieldError{validation.Field, validation.Message}
		}
	}
	return q, nil
}
