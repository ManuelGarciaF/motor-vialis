package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/detour"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

type detourRequest struct {
	Route     *route.Route      `json:"route"`
	Cut       *route.LineString `json:"cut"`
	Criterion *detour.Criterion `json:"criterion"`
}

type detourResponse struct {
	Variant        route.Route             `json:"variant"`
	UncoveredStops []uncoveredStopResponse `json:"uncoveredStops"`
	Comparison     simulation.Comparison   `json:"comparison"`
	Trace          detourTraceResponse     `json:"trace"`
}

type uncoveredStopResponse struct {
	StopOrder           int                   `json:"stopOrder"`
	StopID              string                `json:"stopId"`
	Reason              string                `json:"reason"`
	DistanceToCutMeters float64               `json:"distanceToCutMeters"`
	Baseline            simulation.StopResult `json:"baseline"`
}

type detourTraceResponse struct {
	Decision         detourDecisionResponse `json:"decision"`
	GraphLoadID      int64                  `json:"graphLoadId"`
	BlockedStreetIDs []int64                `json:"blockedStreetIds"`
	SelectedEdgeIDs  []int64                `json:"selectedEdgeIds"`
	Traffic          trafficTraceResponse   `json:"traffic"`
	Graph            graphTraceResponse     `json:"graph"`
	Stops            stopTraceResponse      `json:"stops"`
}

type detourDecisionResponse struct {
	Criterion            detour.Criterion `json:"criterion"`
	TrafficTravelSeconds float64          `json:"trafficTravelSeconds"`
}

type trafficTraceResponse struct {
	FetchedAt         time.Time `json:"fetchedAt"`
	TrafficAgeSeconds float64   `json:"trafficAgeSeconds"`
	Zoom              int       `json:"zoom"`
	TileCount         int       `json:"tileCount"`
	CacheHits         int       `json:"cacheHits"`
}

type graphTraceResponse struct {
	CandidateEdges        int `json:"candidateEdges"`
	BlockedEdges          int `json:"blockedEdges"`
	ForwardDirectEdges    int `json:"forwardDirectEdges"`
	ReverseDirectEdges    int `json:"reverseDirectEdges"`
	ForwardEstimatedEdges int `json:"forwardEstimatedEdges"`
	ReverseEstimatedEdges int `json:"reverseEstimatedEdges"`
}

type stopTraceResponse struct {
	Forced   int `json:"forced"`
	Optional int `json:"optional"`
	Omitted  int `json:"omitted"`
}

func (handler *Handler) createDetour(writer http.ResponseWriter, request *http.Request) {
	var body detourRequest
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
	for _, required := range []struct {
		field   string
		missing bool
	}{
		{field: "route", missing: body.Route == nil},
		{field: "cut", missing: body.Cut == nil},
		{field: "criterion", missing: body.Criterion == nil},
	} {
		if required.missing {
			writeError(
				writer,
				http.StatusBadRequest,
				errorCodeValidation,
				required.field,
				"is required",
			)
			return
		}
	}

	ctx, cancel := handler.withTimeout(request)
	defer cancel()
	result, err := handler.detours.Plan(ctx, detour.Input{
		Route:     *body.Route,
		Cut:       detour.Cut{LineString: *body.Cut},
		Criterion: *body.Criterion,
	})
	if err != nil {
		handler.writeDetourError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, newDetourResponse(result))
}

func newDetourResponse(result detour.Result) detourResponse {
	uncovered := make([]uncoveredStopResponse, len(result.Uncovered))
	for index, stop := range result.Uncovered {
		reason := "criterion_omission"
		if stop.Availability == detour.StopForcedUnavailable {
			reason = "cut_reached"
		}
		uncovered[index] = uncoveredStopResponse{
			StopOrder:           stop.StopOrder,
			StopID:              stop.StopID,
			Reason:              reason,
			DistanceToCutMeters: stop.DistanceMeters,
			Baseline:            stop.Baseline,
		}
	}
	trace := result.Trace
	return detourResponse{
		Variant:        result.Variant,
		UncoveredStops: uncovered,
		Comparison:     result.Comparison,
		Trace: detourTraceResponse{
			Decision: detourDecisionResponse{
				Criterion:            trace.Criterion,
				TrafficTravelSeconds: trace.DecisionTravelSeconds,
			},
			GraphLoadID:      trace.GraphLoadID,
			BlockedStreetIDs: append([]int64{}, trace.BlockedStreetIDs...),
			SelectedEdgeIDs:  append([]int64{}, trace.SelectedEdgeIDs...),
			Traffic: trafficTraceResponse{
				FetchedAt:         trace.Traffic.FetchedAt,
				TrafficAgeSeconds: trace.Traffic.TrafficAge.Seconds(),
				Zoom:              trace.Traffic.Zoom,
				TileCount:         trace.Traffic.TileCount,
				CacheHits:         trace.Traffic.CacheHits,
			},
			Graph: graphTraceResponse{
				CandidateEdges:        trace.Graph.CandidateEdges,
				BlockedEdges:          trace.Graph.BlockedEdges,
				ForwardDirectEdges:    trace.Graph.ForwardDirectEdges,
				ReverseDirectEdges:    trace.Graph.ReverseDirectEdges,
				ForwardEstimatedEdges: trace.Graph.ForwardEstimatedEdges,
				ReverseEstimatedEdges: trace.Graph.ReverseEstimatedEdges,
			},
			Stops: stopTraceResponse{
				Forced:   trace.ForcedStops,
				Optional: trace.OptionalStops,
				Omitted:  trace.OmittedStops,
			},
		},
	}
}

func (handler *Handler) writeDetourError(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
) {
	var routeValidation *route.ValidationError
	if errors.As(err, &routeValidation) {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			routeValidation.Field,
			routeValidation.Message,
		)
		return
	}
	var detourValidation *detour.ValidationError
	if errors.As(err, &detourValidation) {
		writeError(
			writer,
			http.StatusBadRequest,
			errorCodeValidation,
			detourValidation.Field,
			detourValidation.Message,
		)
		return
	}

	if errors.Is(request.Context().Err(), context.Canceled) {
		handler.logger.Info("client cancelled request", "operation", "create detour")
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		handler.logger.Warn("create detour", "error", err)
		writeError(
			writer,
			http.StatusGatewayTimeout,
			errorCodeTimeout,
			"",
			"the detour calculation took longer than the server allows",
		)
		return
	}

	var domainError *detour.Error
	if errors.As(err, &domainError) {
		status := http.StatusInternalServerError
		switch domainError.Kind {
		case detour.ErrorKindInvalidInput:
			status = http.StatusBadRequest
		case detour.ErrorKindNoSolution:
			status = http.StatusUnprocessableEntity
		case detour.ErrorKindDependency:
			status = http.StatusServiceUnavailable
		}
		handler.logger.Info("detour request rejected",
			"code", domainError.Code,
			"kind", domainError.Kind,
		)
		writeError(writer, status, string(domainError.Code), "", domainError.Message)
		return
	}

	handler.logger.Error("create detour", "error", err)
	writeError(writer, http.StatusInternalServerError, errorCodeInternal, "", "internal error")
}
