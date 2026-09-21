package detour

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
)

const (
	anchorMaximumDistanceMeters = 30.0
	stopMaximumDistanceMeters   = 50.0
	locationTolerance           = 1e-9
)

// TrafficProvider supplies one provider-neutral snapshot for a deterministic
// set of Web Mercator tiles.
type TrafficProvider interface {
	Snapshot(context.Context, []traffic.Tile) (traffic.Snapshot, error)
}

// Comparator evaluates the original and generated routes with the stable
// simulation model.
type Comparator interface {
	Compare(context.Context, simulation.ComparisonInput) (simulation.Comparison, error)
}

// Result is the complete RF05 outcome before an HTTP representation is chosen.
type Result struct {
	Variant    route.Route
	Uncovered  []UncoveredStop
	Comparison simulation.Comparison
	Trace      Trace
}

// UncoveredStop identifies one removed stop and its baseline contribution.
type UncoveredStop struct {
	StopOrder      int
	StopID         string
	Availability   StopAvailability
	DistanceMeters float64
	Baseline       simulation.StopResult
}

// Trace reports the data and decisions used to generate a variant.
type Trace struct {
	Criterion             Criterion
	DecisionTravelSeconds float64
	GraphLoadID           int64
	BlockedStreetIDs      []int64
	SelectedEdgeIDs       []int64
	Traffic               TrafficTrace
	Graph                 GraphTrace
	ForcedStops           int
	OptionalStops         int
	OmittedStops          int
}

// TrafficTrace is snapshot provenance without exposing provider payloads.
type TrafficTrace struct {
	FetchedAt  time.Time
	TrafficAge time.Duration
	Zoom       int
	TileCount  int
	CacheHits  int
}

// Service orchestrates cut analysis, traffic routing, route reconstruction,
// validation, and stable simulation comparison.
type Service struct {
	repository      Repository
	trafficProvider TrafficProvider
	comparator      Comparator
	policy          Policy
}

// NewService creates the RF05 orchestrator.
func NewService(
	repository Repository,
	trafficProvider TrafficProvider,
	comparator Comparator,
	policy Policy,
) (*Service, error) {
	if repository == nil || trafficProvider == nil || comparator == nil {
		return nil, fmt.Errorf("detour service dependencies must not be nil")
	}
	if err := policy.validate(); err != nil {
		return nil, err
	}
	return &Service{
		repository:      repository,
		trafficProvider: trafficProvider,
		comparator:      comparator,
		policy:          policy,
	}, nil
}

// Plan builds and evaluates the best local variant for one road cut.
func (service *Service) Plan(ctx context.Context, input Input) (Result, error) {
	if err := route.Validate(input.Route); err != nil {
		return Result{}, err
	}
	if err := ValidateCut(input.Cut, service.policy); err != nil {
		return Result{}, err
	}
	if err := validateCriterion(input.Criterion); err != nil {
		return Result{}, err
	}

	analysis, err := service.repository.Analyze(ctx, input.Route, input.Cut, service.policy)
	if err != nil {
		return Result{}, err
	}
	if len(analysis.Intervals) == 0 {
		return Result{}, fmt.Errorf("analyze detour: affected route has no search-area intervals")
	}
	classified := ClassifyStops(input.Route, input.Cut, service.policy)
	tiles, err := traffic.TilesForGeoJSON(analysis.SearchArea, service.policy.TrafficZoom)
	if err != nil {
		return Result{}, fmt.Errorf("calculate detour traffic tiles: %w", err)
	}
	if len(tiles) > service.policy.MaximumTrafficTiles {
		return Result{}, &Error{
			Code: ErrorTrafficTileLimitExceeded,
			Kind: ErrorKindInvalidInput,
			Message: fmt.Sprintf(
				"search area requires %d traffic tiles, maximum is %d",
				len(tiles),
				service.policy.MaximumTrafficTiles,
			),
		}
	}
	snapshot, err := service.trafficProvider.Snapshot(ctx, tiles)
	if err != nil {
		return Result{}, mapTrafficError(err)
	}

	plans, points, pairs, err := buildIntervalPlans(input.Route, classified, analysis.Intervals)
	if err != nil {
		return Result{}, err
	}
	routing, err := service.repository.Route(ctx, RoutingRequest{
		Analysis: analysis,
		Traffic:  snapshot.Segments,
		Points:   points,
		Pairs:    pairs,
	}, service.policy)
	if err != nil {
		return Result{}, err
	}

	omitted := make(map[int]struct{})
	var replacements []routeReplacement
	var decisionSeconds float64
	var selectedEdgeIDs []int64
	for planIndex := range plans {
		selection, selectErr := selectInterval(plans[planIndex], routing.Paths, input.Criterion)
		if selectErr != nil {
			if topologyCanResolve(plans[planIndex], routing.TopologyAvailablePairs, input.Criterion) {
				return Result{}, &Error{
					Code:    ErrorTrafficCoverageInsufficient,
					Kind:    ErrorKindDependency,
					Message: fmt.Sprintf("traffic coverage cannot connect affected interval %d", planIndex),
					Cause:   selectErr,
				}
			}
			return Result{}, selectErr
		}
		decisionSeconds += selection.TravelSeconds
		for _, localOrder := range selection.OmittedStopOrders {
			if stopOrder := plans[planIndex].nodes[localOrder].stopOrder; stopOrder >= 0 {
				omitted[stopOrder] = struct{}{}
			}
		}
		for stopOrder := range plans[planIndex].boundaryOmitted {
			omitted[stopOrder] = struct{}{}
		}
		for _, connection := range selection.Connections {
			from := plans[planIndex].nodes[connection.FromStopOrder]
			to := plans[planIndex].nodes[connection.ToStopOrder]
			replacements = append(replacements, routeReplacement{
				start: from.location,
				end:   to.location,
				path:  connection.Path,
			})
			selectedEdgeIDs = append(selectedEdgeIDs, connection.EdgeIDs...)
		}
	}

	variant, err := reconstructRoute(input.Route, omitted, replacements)
	if err != nil {
		return Result{}, err
	}
	if err := route.Validate(variant); err != nil {
		// This route was generated by the engine, so it is an internal failure,
		// not an input validation error attributable to the caller.
		return Result{}, fmt.Errorf("validate generated detour variant: %v", err)
	}
	comparison, err := service.comparator.Compare(ctx, simulation.ComparisonInput{
		Baseline: input.Route,
		Proposed: variant,
	})
	if err != nil {
		return Result{}, fmt.Errorf("compare detour variant: %w", err)
	}

	return Result{
		Variant:    variant,
		Uncovered:  uncoveredStops(classified, omitted, comparison.Baseline.ByStop),
		Comparison: comparison,
		Trace: Trace{
			Criterion:             input.Criterion,
			DecisionTravelSeconds: decisionSeconds,
			GraphLoadID:           analysis.GraphLoadID,
			BlockedStreetIDs:      append([]int64(nil), analysis.BlockedStreetIDs...),
			SelectedEdgeIDs:       selectedEdgeIDs,
			Traffic: TrafficTrace{
				FetchedAt:  snapshot.FetchedAt,
				TrafficAge: snapshot.TrafficAge,
				Zoom:       snapshot.Zoom,
				TileCount:  snapshot.TileCount,
				CacheHits:  snapshot.CacheHits,
			},
			Graph:         routing.Trace,
			ForcedStops:   countAvailability(classified, StopForcedUnavailable),
			OptionalStops: countAvailability(classified, StopOptional),
			OmittedStops:  len(omitted),
		},
	}, nil
}

type routeLocation struct {
	segmentOrder int
	fraction     float64
}

func (location routeLocation) scalar() float64 {
	return float64(location.segmentOrder) + location.fraction
}

type intervalNode struct {
	point     RoutingPoint
	location  routeLocation
	stopOrder int
}

type intervalPlan struct {
	nodes           []intervalNode
	stops           []ClassifiedStop
	boundaryOmitted map[int]struct{}
}

func buildIntervalPlans(
	input route.Route,
	classified []ClassifiedStop,
	intervals []AffectedInterval,
) ([]intervalPlan, []RoutingPoint, []PointPair, error) {
	plans := make([]intervalPlan, 0, len(intervals))
	var points []RoutingPoint
	var pairs []PointPair
	nextPointID := int64(1)
	lastScalar := float64(len(input.Stops) - 1)

	for _, interval := range intervals {
		entry := routeLocation{interval.Entry.SegmentOrder, interval.Entry.Fraction}
		exit := routeLocation{interval.Exit.SegmentOrder, interval.Exit.Fraction}
		if entry.scalar() >= exit.scalar()-locationTolerance {
			return nil, nil, nil, fmt.Errorf("detour interval %d is empty or reversed", interval.Order)
		}
		plan := intervalPlan{boundaryOmitted: make(map[int]struct{})}
		entryIsRouteStart := entry.scalar() <= locationTolerance
		exitIsRouteEnd := exit.scalar() >= lastScalar-locationTolerance
		if !entryIsRouteStart {
			appendAnchorNode(&plan, &points, &nextPointID, interval.Entry)
		}

		for stopOrder, stop := range classified {
			scalar := float64(stopOrder)
			if scalar < entry.scalar()-locationTolerance || scalar > exit.scalar()+locationTolerance {
				continue
			}
			atEntry := math.Abs(scalar-entry.scalar()) <= locationTolerance
			atExit := math.Abs(scalar-exit.scalar()) <= locationTolerance
			if (atEntry && !entryIsRouteStart) || (atExit && !exitIsRouteEnd) {
				if stop.Availability == StopForcedUnavailable {
					plan.boundaryOmitted[stopOrder] = struct{}{}
				}
				continue
			}

			node := intervalNode{
				location:  stopLocation(stopOrder, len(input.Stops)),
				stopOrder: stopOrder,
			}
			plan.stops = append(plan.stops, ClassifiedStop{
				Order:          len(plan.stops),
				ID:             stop.ID,
				DistanceMeters: stop.DistanceMeters,
				Availability:   stop.Availability,
			})
			if stop.Availability != StopForcedUnavailable {
				node.point = RoutingPoint{
					ID:                    nextPointID,
					Position:              input.Stops[stopOrder].Position,
					DirectionDegrees:      stopDirection(input, stopOrder),
					MaximumDistanceMeters: stopMaximumDistanceMeters,
					Role:                  pointRole(stop.Availability),
				}
				nextPointID++
				points = append(points, node.point)
			}
			plan.nodes = append(plan.nodes, node)
		}
		if !exitIsRouteEnd {
			appendAnchorNode(&plan, &points, &nextPointID, interval.Exit)
		}
		if len(plan.nodes) < 2 {
			return nil, nil, nil, &Error{
				Code:    ErrorNoDetourWithinSearchArea,
				Kind:    ErrorKindNoSolution,
				Message: fmt.Sprintf("affected interval %d has fewer than two routing states", interval.Order),
			}
		}
		pairs = append(pairs, intervalPairs(plan)...)
		plans = append(plans, plan)
	}
	return plans, points, pairs, nil
}

func appendAnchorNode(
	plan *intervalPlan,
	points *[]RoutingPoint,
	nextPointID *int64,
	anchor Anchor,
) {
	point := RoutingPoint{
		ID:                    *nextPointID,
		Position:              anchor.Position,
		DirectionDegrees:      anchor.DirectionDegrees,
		MaximumDistanceMeters: anchorMaximumDistanceMeters,
		Role:                  PointAnchor,
	}
	*nextPointID = *nextPointID + 1
	plan.nodes = append(plan.nodes, intervalNode{
		point:     point,
		location:  routeLocation{anchor.SegmentOrder, anchor.Fraction},
		stopOrder: -1,
	})
	plan.stops = append(plan.stops, ClassifiedStop{
		Order:        len(plan.stops),
		ID:           fmt.Sprintf("@anchor-%d", point.ID),
		Availability: StopRequired,
	})
	*points = append(*points, point)
}

func intervalPairs(plan intervalPlan) []PointPair {
	var pairs []PointPair
	for from := 0; from < len(plan.nodes); from++ {
		if plan.nodes[from].point.ID == 0 {
			continue
		}
		for to := from + 1; to < len(plan.nodes); to++ {
			if plan.nodes[to].point.ID == 0 || containsRequired(plan.stops[from+1:to]) {
				continue
			}
			pairs = append(pairs, PointPair{
				From: plan.nodes[from].point.ID,
				To:   plan.nodes[to].point.ID,
			})
		}
	}
	return pairs
}

func selectInterval(
	plan intervalPlan,
	paths []GraphPath,
	criterion Criterion,
) (Selection, error) {
	pathByPair := make(map[PointPair]GraphPath, len(paths))
	for _, path := range paths {
		pathByPair[PointPair{From: path.From, To: path.To}] = path
	}
	var connections []Connection
	for from := 0; from < len(plan.nodes); from++ {
		for to := from + 1; to < len(plan.nodes); to++ {
			path, found := pathByPair[PointPair{
				From: plan.nodes[from].point.ID,
				To:   plan.nodes[to].point.ID,
			}]
			if !found {
				continue
			}
			connections = append(connections, Connection{
				FromStopOrder: from,
				ToStopOrder:   to,
				TravelSeconds: path.TravelSeconds,
				EdgeIDs:       path.EdgeIDs,
				Path:          path.Geometry,
			})
		}
	}
	return Select(plan.stops, connections, criterion)
}

func topologyCanResolve(plan intervalPlan, pairs []PointPair, criterion Criterion) bool {
	available := make(map[PointPair]struct{}, len(pairs))
	for _, pair := range pairs {
		available[pair] = struct{}{}
	}
	var connections []Connection
	for from := 0; from < len(plan.nodes); from++ {
		for to := from + 1; to < len(plan.nodes); to++ {
			pair := PointPair{From: plan.nodes[from].point.ID, To: plan.nodes[to].point.ID}
			if _, found := available[pair]; !found {
				continue
			}
			connections = append(connections, Connection{
				FromStopOrder: from,
				ToStopOrder:   to,
			})
		}
	}
	_, err := Select(plan.stops, connections, criterion)
	return err == nil
}

func pointRole(availability StopAvailability) PointRole {
	if availability == StopRequired {
		return PointRequiredStop
	}
	return PointOptionalStop
}

func stopLocation(order, stopCount int) routeLocation {
	if order == stopCount-1 {
		return routeLocation{segmentOrder: order - 1, fraction: 1}
	}
	return routeLocation{segmentOrder: order, fraction: 0}
}

func mapTrafficError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var providerError *traffic.Error
	if !errors.As(err, &providerError) {
		return &Error{
			Code:    ErrorTrafficUnavailable,
			Kind:    ErrorKindDependency,
			Message: "traffic snapshot is unavailable",
			Cause:   err,
		}
	}
	code := ErrorTrafficUnavailable
	kind := ErrorKindDependency
	switch providerError.Code {
	case traffic.ErrorStale:
		code = ErrorTrafficStale
	case traffic.ErrorTileLimit:
		code = ErrorTrafficTileLimitExceeded
		kind = ErrorKindInvalidInput
	}
	return &Error{Code: code, Kind: kind, Message: providerError.Message, Cause: err}
}

func countAvailability(stops []ClassifiedStop, availability StopAvailability) int {
	count := 0
	for _, stop := range stops {
		if stop.Availability == availability {
			count++
		}
	}
	return count
}

func uncoveredStops(
	classified []ClassifiedStop,
	omitted map[int]struct{},
	baseline []simulation.StopResult,
) []UncoveredStop {
	result := make([]UncoveredStop, 0, len(omitted))
	for order, stop := range classified {
		if _, found := omitted[order]; !found {
			continue
		}
		var contribution simulation.StopResult
		if order < len(baseline) {
			contribution = baseline[order]
		}
		result = append(result, UncoveredStop{
			StopOrder:      order,
			StopID:         stop.ID,
			Availability:   stop.Availability,
			DistanceMeters: stop.DistanceMeters,
			Baseline:       contribution,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].StopOrder < result[right].StopOrder
	})
	return result
}
