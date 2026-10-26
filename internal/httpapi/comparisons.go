package httpapi

import (
	"context"
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
	handler.compareRoutes(writer, request, "compare routes", handler.comparator.Compare)
}

// createDetourComparison compares an edited detour variant against its
// baseline. Retained stops keep the baseline order and new stops may be added;
// fares are charged by distance along the baseline route.
func (handler *Handler) createDetourComparison(writer http.ResponseWriter, request *http.Request) {
	handler.compareRoutes(writer, request, "compare detour", handler.comparator.CompareDetour)
}

func (handler *Handler) compareRoutes(
	writer http.ResponseWriter,
	request *http.Request,
	operation string,
	compare func(context.Context, simulation.ComparisonInput) (simulation.Comparison, error),
) {
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

	comparison, err := compare(ctx, simulation.ComparisonInput{
		Baseline: *body.Baseline,
		Proposed: *body.Proposed,
	})
	if err != nil {
		handler.writeEstimationError(writer, request, operation, err)
		return
	}

	writeJSON(writer, http.StatusOK, comparison)
}
