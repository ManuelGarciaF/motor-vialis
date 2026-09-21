package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// similarLinesRequest decodes into pointers for the same reason
// comparisonRequest does: an absent route is a caller who forgot the field,
// while an empty one is a caller who sent something wrong, and only the first
// is worth naming as missing.
//
// The limit is a pointer so an explicit zero can be refused. Left out, it
// means "use the default"; sent as 0 or as a negative number, it is a client
// computing a page size wrongly, and silently answering with ten results would
// hide the bug rather than report it.
type similarLinesRequest struct {
	Route *route.Route `json:"route"`
	Limit *int         `json:"limit"`
}

// findSimilarLines answers which stored lines already run where the caller
// drew.
//
// It is a POST although it reads nothing: the drawn route is a whole geometry,
// which does not fit in a query string, and the same body shape is what
// /simulations and /comparisons already take.
func (handler *Handler) findSimilarLines(
	writer http.ResponseWriter,
	request *http.Request,
) {
	var body similarLinesRequest
	if err := decodeBody(request, &body); err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			"",
			fmt.Sprintf("invalid JSON body: %v", err),
		)
		return
	}
	if body.Route == nil {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			"route",
			"is required",
		)
		return
	}
	if body.Limit != nil && *body.Limit <= 0 {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			"limit",
			"must be a positive integer",
		)
		return
	}

	query := lines.SimilarityRequest{Route: *body.Route}
	if body.Limit != nil {
		query.Limit = *body.Limit
	}

	ctx, cancel := handler.withTimeout(request)
	defer cancel()

	similar, err := handler.lines.FindSimilar(ctx, query)
	if err != nil {
		// A route the caller drew is the caller's to fix, so its field errors
		// are reported the way /simulations and /comparisons report theirs;
		// everything else is a failure of the lookup.
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
		handler.writeLineError(writer, request, "find similar stored lines", err)
		return
	}

	writeJSON(writer, http.StatusOK, similar)
}
