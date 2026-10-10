package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
)

func (h *Handler) getBunchingRecurrence(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, errorCodeValidation, "query", "this endpoint accepts no query parameters")
		return
	}
	if h.bunching == nil {
		writeError(w, http.StatusServiceUnavailable, "bunching_unavailable", "", "bunching is not configured")
		return
	}
	ctx, cancel := h.withTimeout(r)
	defer cancel()
	page, err := h.bunching.Recurrence(ctx)
	if err != nil {
		h.writeLineError(w, r, "read bunching recurrence", err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) listBunching(w http.ResponseWriter, r *http.Request) {
	query, err := parseBunchingQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, errorCodeValidation, err.Field, err.Message)
		return
	}
	if h.bunching == nil {
		writeError(w, http.StatusServiceUnavailable, "bunching_unavailable", "", "bunching is not configured")
		return
	}
	ctx, cancel := h.withTimeout(r)
	defer cancel()
	page, serviceErr := h.bunching.List(ctx, query)
	if serviceErr != nil {
		var validation *bunching.QueryError
		if errors.As(serviceErr, &validation) {
			writeError(w, http.StatusBadRequest, errorCodeValidation, validation.Field, validation.Message)
			return
		}
		h.writeLineError(w, r, "list bunching events", serviceErr)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func parseBunchingQuery(r *http.Request) (bunching.HistoryQuery, *fieldError) {
	// URL.Query silently discards malformed query values; reject them instead.
	values, parseErr := url.ParseQuery(r.URL.RawQuery)
	if parseErr != nil {
		return bunching.HistoryQuery{}, &fieldError{Field: "query", Message: "invalid query encoding"}
	}
	for key, entries := range values {
		if key != "from" && key != "to" && key != "state" && key != "limit" && key != "offset" {
			return bunching.HistoryQuery{}, &fieldError{Field: key, Message: "unknown query parameter"}
		}
		if len(entries) != 1 {
			return bunching.HistoryQuery{}, &fieldError{Field: key, Message: "must appear only once"}
		}
	}
	query := bunching.HistoryQuery{State: values.Get("state")}
	for _, field := range []string{"from", "to"} {
		if _, ok := values[field]; ok {
			parsed, err := time.Parse(time.RFC3339, values.Get(field))
			if err != nil {
				return query, &fieldError{Field: field, Message: "must be an RFC3339 timestamp"}
			}
			if field == "from" {
				query.From = &parsed
			} else {
				query.To = &parsed
			}
		}
	}
	var err *fieldError
	query.Limit, err = parseCount(values.Get("limit"), "limit")
	if err != nil {
		return query, err
	}
	if _, provided := values["limit"]; provided && query.Limit == 0 {
		return query, &fieldError{Field: "limit", Message: "must be a positive integer"}
	}
	query.Offset, err = parseCount(values.Get("offset"), "offset")
	if err != nil {
		return query, err
	}
	return query, nil
}

func (h *Handler) getBunching(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, errorCodeValidation, "id", "must be a positive integer")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, errorCodeValidation, "query", "this endpoint accepts no query parameters")
		return
	}
	if h.bunching == nil {
		writeError(w, http.StatusServiceUnavailable, "bunching_unavailable", "", "bunching is not configured")
		return
	}
	ctx, cancel := h.withTimeout(r)
	defer cancel()
	current, err := h.bunching.Get(ctx, id)
	if err != nil {
		h.writeLineError(w, r, "read current bunching", err)
		return
	}
	writeJSON(w, http.StatusOK, current)
}
