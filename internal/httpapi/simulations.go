package httpapi

import (
	"fmt"
	"net/http"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
)

func (handler *Handler) createSimulation(writer http.ResponseWriter, request *http.Request) {
	var input simulation.Route
	if err := decodeBody(request, &input); err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			"",
			fmt.Sprintf("invalid JSON body: %v", err),
		)
		return
	}

	ctx, cancel := handler.withTimeout(request)
	defer cancel()

	result, err := handler.simulator.Simulate(ctx, input)
	if err != nil {
		handler.writeEstimationError(writer, request, "simulate route", err)
		return
	}

	writeJSON(writer, http.StatusOK, result)
}
