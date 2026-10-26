package httpapi

import (
	"fmt"
	"net/http"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

type segmentRouteRequest struct {
	Waypoints          []route.Position       `json:"waypoints"`
	Avoid              *route.MultiLineString `json:"avoid"`
	OriginBearing      *float64               `json:"originBearing"`
	DestinationBearing *float64               `json:"destinationBearing"`
}

type segmentRouteResponse struct {
	Geometry       route.LineString `json:"geometry"`
	DistanceMeters float64          `json:"distanceMeters"`
}

func (handler *Handler) createSegmentRoute(writer http.ResponseWriter, request *http.Request) {
	var body segmentRouteRequest
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
	if handler.segments == nil {
		writeError(writer, http.StatusServiceUnavailable, "segment_routing_unavailable", "", "segment routing is not configured")
		return
	}

	input := detour.SegmentInput{
		Waypoints:          body.Waypoints,
		OriginBearing:      body.OriginBearing,
		DestinationBearing: body.DestinationBearing,
	}
	if body.Avoid != nil {
		input.Avoid = &detour.Cut{Lines: body.Avoid.Lines}
	}

	ctx, cancel := handler.withTimeout(request)
	defer cancel()

	result, err := handler.segments.Route(ctx, input)
	if err != nil {
		handler.writeDetourError(writer, request, err)
		return
	}

	writeJSON(writer, http.StatusOK, segmentRouteResponse{
		Geometry:       result.Geometry,
		DistanceMeters: result.DistanceMeters,
	})
}
