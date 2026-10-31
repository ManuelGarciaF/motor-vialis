package httpapi

import (
	"fmt"
	"net/http"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

type cutPathRequest struct {
	Waypoints []route.Position `json:"waypoints"`
}

// createCutPath answers with the same body as POST /routes/segment: the street
// path through the waypoints, ignoring one-way directions.
func (handler *Handler) createCutPath(writer http.ResponseWriter, request *http.Request) {
	var body cutPathRequest
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
	if body.Waypoints == nil {
		writeError(writer, http.StatusBadRequest, errorCodeValidation, "waypoints", "is required")
		return
	}
	if handler.cutPaths == nil {
		writeError(writer, http.StatusServiceUnavailable, "cut_path_unavailable", "", "cut path routing is not configured")
		return
	}

	ctx, cancel := handler.withTimeout(request)
	defer cancel()

	result, err := handler.cutPaths.Route(ctx, detour.CutPathInput{Waypoints: body.Waypoints})
	if err != nil {
		handler.writeDetourError(writer, request, err)
		return
	}

	writeJSON(writer, http.StatusOK, segmentRouteResponse{
		Geometry:       result.Geometry,
		DistanceMeters: result.DistanceMeters,
	})
}
