package httpapi

import (
	"fmt"
	"net/http"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
)

// Pointer fields distinguish missing routes from present but invalid routes.
type comparisonRequest struct {
	Baseline *simulation.Route `json:"baseline"`
	Proposed *simulation.Route `json:"proposed"`
}

func (handler *Handler) createComparison(writer http.ResponseWriter, request *http.Request) {
	var body comparisonRequest
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
	required := []struct {
		field string
		value *simulation.Route
	}{
		{"baseline", body.Baseline},
		{"proposed", body.Proposed},
	}
	for _, route := range required {
		if route.value == nil {
			writeError(
				writer,
				http.StatusBadRequest,
				errorCodeValidation,
				route.field,
				"is required",
			)
			return
		}
	}

	ctx, cancel := handler.withTimeout(request)
	defer cancel()

	comparison, err := handler.comparator.Compare(ctx, simulation.ComparisonInput{
		Baseline: *body.Baseline,
		Proposed: *body.Proposed,
	})
	if err != nil {
		handler.writeEstimationError(writer, request, "compare routes", err)
		return
	}

	writeJSON(writer, http.StatusOK, comparison)
}
