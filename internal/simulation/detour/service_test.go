package detour

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
)

func TestServicePlanAppliesCriterionAndComparesCompleteVariant(t *testing.T) {
	input := serviceTestInput()
	analysis := serviceTestAnalysis()

	cases := []struct {
		name        string
		criterion   Criterion
		wantStops   []string
		wantOmitted []int
		wantSeconds float64
	}{
		{
			name:        "shortest time omits optional stops",
			criterion:   CriterionShortestTime,
			wantStops:   []string{"A", "D"},
			wantOmitted: []int{1, 2},
			wantSeconds: 14.9,
		},
		{
			name:        "fewest lost stops retains optional stops",
			criterion:   CriterionFewestLostStops,
			wantStops:   []string{"A", "B", "C", "D"},
			wantSeconds: 15,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &scriptedDetourRepository{
				analysis: analysis,
				routeResult: func(request RoutingRequest) RoutingResult {
					return fullyConnectedRouting(request)
				},
			}
			provider := &scriptedTrafficProvider{snapshot: traffic.Snapshot{
				Segments:   []traffic.Segment{{SourceID: "fixture"}},
				FetchedAt:  time.Unix(100, 0),
				TrafficAge: time.Minute,
				Zoom:       14,
				TileCount:  4,
				CacheHits:  2,
			}}
			comparator := &recordingComparator{}
			service, err := NewService(repository, provider, comparator, serviceTestPolicy())
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			input.Criterion = testCase.criterion

			got, err := service.Plan(context.Background(), input)
			if err != nil {
				t.Fatalf("Plan() error = %v", err)
			}
			gotStopIDs := make([]string, len(got.Variant.Stops))
			for index, stop := range got.Variant.Stops {
				gotStopIDs[index] = stop.ID
			}
			if !reflect.DeepEqual(gotStopIDs, testCase.wantStops) {
				t.Fatalf("variant stops = %v, want %v", gotStopIDs, testCase.wantStops)
			}
			gotOmitted := make([]int, len(got.Uncovered))
			for index, stop := range got.Uncovered {
				gotOmitted[index] = stop.StopOrder
				if stop.Baseline.StopID != input.Route.Stops[stop.StopOrder].ID {
					t.Fatalf("uncovered baseline = %#v", stop.Baseline)
				}
			}
			if !slices.Equal(gotOmitted, testCase.wantOmitted) {
				t.Fatalf("omitted = %v, want %v", gotOmitted, testCase.wantOmitted)
			}
			if got.Trace.DecisionTravelSeconds != testCase.wantSeconds {
				t.Fatalf("decision seconds = %v, want %v", got.Trace.DecisionTravelSeconds, testCase.wantSeconds)
			}
			if err := route.Validate(got.Variant); err != nil {
				t.Fatalf("variant is invalid: %v", err)
			}
			if comparator.input.Baseline.Stops[0].ID != "A" ||
				len(comparator.input.Proposed.Stops) != len(testCase.wantStops) {
				t.Fatalf("comparison input = %#v", comparator.input)
			}
			if len(provider.tiles) == 0 || repository.routeCalls != 1 {
				t.Fatalf("traffic tiles = %v, route calls = %d", provider.tiles, repository.routeCalls)
			}
		})
	}
}

func TestServicePlanCanOmitOptionalTerminalStops(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime
	analysis := serviceTestAnalysis()
	analysis.Intervals = []AffectedInterval{{
		Order: 0,
		Entry: Anchor{SegmentOrder: 0, Fraction: 0, Position: input.Route.Stops[0].Position, DirectionDegrees: 90},
		Exit:  Anchor{SegmentOrder: 2, Fraction: 1, Position: input.Route.Stops[3].Position, DirectionDegrees: 90},
	}}
	policy := serviceTestPolicy()
	policy.ForcedStopRadiusMeters = 1
	policy.OptionalStopRadiusMeters = 600
	repository := &scriptedDetourRepository{
		analysis: analysis,
		routeResult: func(request RoutingRequest) RoutingResult {
			result := fullyConnectedRouting(request)
			for index := range result.Paths {
				result.Paths[index].TravelSeconds = 100
				if result.Paths[index].From == 2 && result.Paths[index].To == 3 {
					result.Paths[index].TravelSeconds = 1
				}
			}
			return result
		},
	}
	service, err := NewService(
		repository,
		&scriptedTrafficProvider{snapshot: traffic.Snapshot{Segments: []traffic.Segment{{SourceID: "fixture"}}}},
		&recordingComparator{},
		policy,
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	got, err := service.Plan(context.Background(), input)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(got.Variant.Stops) != 2 {
		t.Fatalf("variant stop count = %d, want 2", len(got.Variant.Stops))
	}
	if ids := []string{got.Variant.Stops[0].ID, got.Variant.Stops[1].ID}; !reflect.DeepEqual(ids, []string{"B", "C"}) {
		t.Fatalf("variant stops = %v, want [B C]", ids)
	}
	if !slices.Equal(
		[]int{got.Uncovered[0].StopOrder, got.Uncovered[1].StopOrder},
		[]int{0, 3},
	) {
		t.Fatalf("uncovered = %#v, want terminal stops", got.Uncovered)
	}
}

func TestServicePlanAlwaysOmitsStopReachedByCut(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionFewestLostStops
	input.Cut.LineString.Positions[0].Longitude = input.Route.Stops[1].Position.Longitude
	input.Cut.LineString.Positions[1].Longitude = input.Route.Stops[1].Position.Longitude
	repository := &scriptedDetourRepository{
		analysis: serviceTestAnalysis(),
		routeResult: func(request RoutingRequest) RoutingResult {
			return fullyConnectedRouting(request)
		},
	}
	service, err := NewService(
		repository,
		&scriptedTrafficProvider{snapshot: traffic.Snapshot{Segments: []traffic.Segment{{SourceID: "fixture"}}}},
		&recordingComparator{},
		serviceTestPolicy(),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	got, err := service.Plan(context.Background(), input)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(got.Uncovered) != 1 || got.Uncovered[0].StopID != "B" ||
		got.Uncovered[0].Availability != StopForcedUnavailable {
		t.Fatalf("uncovered = %#v, want forced stop B", got.Uncovered)
	}
}

func TestServicePlanProcessesEveryAffectedInterval(t *testing.T) {
	input := serviceTestInput()
	input.Route.Stops = append(input.Route.Stops, route.Stop{
		ID:       "E",
		Position: route.Position{Latitude: -34.6, Longitude: -58.390},
	})
	input.Route.Stops[3].PathToNext = straightLine(
		input.Route.Stops[3].Position,
		input.Route.Stops[4].Position,
	)
	analysis := serviceTestAnalysis()
	analysis.Intervals = []AffectedInterval{
		{
			Order: 0,
			Entry: Anchor{SegmentOrder: 0, Fraction: 0.2, Position: positionOn(input.Route, 0, 0.2), DirectionDegrees: 90},
			Exit:  Anchor{SegmentOrder: 0, Fraction: 0.8, Position: positionOn(input.Route, 0, 0.8), DirectionDegrees: 90},
		},
		{
			Order: 1,
			Entry: Anchor{SegmentOrder: 3, Fraction: 0.2, Position: positionOn(input.Route, 3, 0.2), DirectionDegrees: 90},
			Exit:  Anchor{SegmentOrder: 3, Fraction: 0.8, Position: positionOn(input.Route, 3, 0.8), DirectionDegrees: 90},
		},
	}
	repository := &scriptedDetourRepository{
		analysis: analysis,
		routeResult: func(request RoutingRequest) RoutingResult {
			return fullyConnectedRouting(request)
		},
	}
	service, err := NewService(
		repository,
		&scriptedTrafficProvider{snapshot: traffic.Snapshot{
			Segments: []traffic.Segment{{SourceID: "fixture"}}, Zoom: 14, TileCount: 4,
		}},
		&recordingComparator{},
		serviceTestPolicy(),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	input.Criterion = CriterionShortestTime

	got, err := service.Plan(context.Background(), input)
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(got.Trace.SelectedEdgeIDs) != 2 {
		t.Fatalf("selected edges = %v, want one per interval", got.Trace.SelectedEdgeIDs)
	}
	middle := got.Variant.Stops[1].PathToNext
	if middle == nil || !reflect.DeepEqual(
		middle.Positions,
		input.Route.Stops[1].PathToNext.Positions,
	) {
		t.Fatalf("unaffected middle segment changed: %#v", middle)
	}
}

func TestServicePlanDistinguishesTrafficCoverageFromTopology(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime
	cases := []struct {
		name          string
		topologyPaths bool
		wantCode      ErrorCode
	}{
		{name: "topology exists", topologyPaths: true, wantCode: ErrorTrafficCoverageInsufficient},
		{name: "topology is isolated", topologyPaths: false, wantCode: ErrorNoDetourWithinSearchArea},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &scriptedDetourRepository{
				analysis: serviceTestAnalysis(),
				routeResult: func(request RoutingRequest) RoutingResult {
					result := RoutingResult{}
					if testCase.topologyPaths {
						result.TopologyAvailablePairs = append(result.TopologyAvailablePairs, request.Pairs...)
					}
					return result
				},
			}
			service, err := NewService(
				repository,
				&scriptedTrafficProvider{snapshot: traffic.Snapshot{Segments: []traffic.Segment{{SourceID: "fixture"}}}},
				&recordingComparator{},
				serviceTestPolicy(),
			)
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}

			_, err = service.Plan(context.Background(), input)
			var detourError *Error
			if !errors.As(err, &detourError) || detourError.Code != testCase.wantCode {
				t.Fatalf("Plan() error = %v, want %s", err, testCase.wantCode)
			}
		})
	}
}

func TestServicePlanMapsTrafficProviderError(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime
	service, err := NewService(
		&scriptedDetourRepository{analysis: serviceTestAnalysis()},
		&scriptedTrafficProvider{err: &traffic.Error{Code: traffic.ErrorStale, Message: "old"}},
		&recordingComparator{},
		serviceTestPolicy(),
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	_, err = service.Plan(context.Background(), input)
	var detourError *Error
	if !errors.As(err, &detourError) || detourError.Code != ErrorTrafficStale {
		t.Fatalf("Plan() error = %v, want traffic_stale", err)
	}
}

type scriptedDetourRepository struct {
	analysis    Analysis
	analyzeErr  error
	routeResult func(RoutingRequest) RoutingResult
	routeErr    error
	routeCalls  int
}

func (repository *scriptedDetourRepository) Analyze(
	context.Context,
	route.Route,
	Cut,
	Policy,
) (Analysis, error) {
	return repository.analysis, repository.analyzeErr
}

func (repository *scriptedDetourRepository) Route(
	_ context.Context,
	request RoutingRequest,
	_ Policy,
) (RoutingResult, error) {
	repository.routeCalls++
	if repository.routeErr != nil {
		return RoutingResult{}, repository.routeErr
	}
	return repository.routeResult(request), nil
}

type scriptedTrafficProvider struct {
	snapshot traffic.Snapshot
	err      error
	tiles    []traffic.Tile
}

func (provider *scriptedTrafficProvider) Snapshot(
	_ context.Context,
	tiles []traffic.Tile,
) (traffic.Snapshot, error) {
	provider.tiles = append([]traffic.Tile(nil), tiles...)
	return provider.snapshot, provider.err
}

type recordingComparator struct {
	input simulation.ComparisonInput
}

func (comparator *recordingComparator) Compare(
	_ context.Context,
	input simulation.ComparisonInput,
) (simulation.Comparison, error) {
	comparator.input = input
	baseline := make([]simulation.StopResult, len(input.Baseline.Stops))
	for order, stop := range input.Baseline.Stops {
		baseline[order] = simulation.StopResult{StopOrder: order, StopID: stop.ID}
	}
	return simulation.Comparison{
		Baseline: simulation.Result{ByStop: baseline},
	}, nil
}

func fullyConnectedRouting(request RoutingRequest) RoutingResult {
	byID := make(map[int64]RoutingPoint, len(request.Points))
	for _, point := range request.Points {
		byID[point.ID] = point
	}
	result := RoutingResult{
		TopologyAvailablePairs: append([]PointPair(nil), request.Pairs...),
		Trace:                  GraphTrace{CandidateEdges: 10},
	}
	for _, pair := range request.Pairs {
		from := byID[pair.From]
		to := byID[pair.To]
		seconds := 100.0
		if pair.To == pair.From+1 {
			seconds = 5
		}
		if pair.From == 1 && pair.To == 4 {
			seconds = 14.9
		}
		midpoint := route.Position{
			Latitude:  (from.Position.Latitude+to.Position.Latitude)/2 + 0.0001,
			Longitude: (from.Position.Longitude + to.Position.Longitude) / 2,
		}
		result.Paths = append(result.Paths, GraphPath{
			From:          pair.From,
			To:            pair.To,
			TravelSeconds: seconds,
			EdgeIDs:       []int64{pair.From*10 + pair.To},
			Geometry: route.LineString{Positions: []route.Position{
				from.Position,
				midpoint,
				to.Position,
			}},
		})
	}
	return result
}

func serviceTestInput() Input {
	stops := []route.Stop{
		{ID: "A", Position: route.Position{Latitude: -34.6, Longitude: -58.406}},
		{ID: "B", Position: route.Position{Latitude: -34.6, Longitude: -58.402}},
		{ID: "C", Position: route.Position{Latitude: -34.6, Longitude: -58.398}},
		{ID: "D", Position: route.Position{Latitude: -34.6, Longitude: -58.394}},
	}
	for index := 0; index < len(stops)-1; index++ {
		stops[index].PathToNext = straightLine(stops[index].Position, stops[index+1].Position)
	}
	return Input{
		Route: route.Route{Jurisdiction: route.JurisdictionCABA, Stops: stops},
		Cut: Cut{LineString: route.LineString{Positions: []route.Position{
			{Latitude: -34.6001, Longitude: -58.400},
			{Latitude: -34.5999, Longitude: -58.400},
		}}},
	}
}

func serviceTestAnalysis() Analysis {
	input := serviceTestInput()
	return Analysis{
		GraphLoadID:      3,
		SearchArea:       []byte(`{"type":"Polygon","coordinates":[[[-58.42,-34.62],[-58.38,-34.62],[-58.38,-34.58],[-58.42,-34.58],[-58.42,-34.62]]]}`),
		ForbiddenArea:    []byte(`{"type":"Polygon","coordinates":[[[-58.401,-34.601],[-58.399,-34.601],[-58.399,-34.599],[-58.401,-34.599],[-58.401,-34.601]]]}`),
		SearchBounds:     Bounds{MinLatitude: -34.62, MinLongitude: -58.42, MaxLatitude: -34.58, MaxLongitude: -58.38},
		BlockedStreetIDs: []int64{99},
		RouteAffected:    true,
		Intervals: []AffectedInterval{{
			Order: 0,
			Entry: Anchor{SegmentOrder: 0, Fraction: 0.5, Position: positionOn(input.Route, 0, 0.5), DirectionDegrees: 90},
			Exit:  Anchor{SegmentOrder: 2, Fraction: 0.5, Position: positionOn(input.Route, 2, 0.5), DirectionDegrees: 90},
		}},
	}
}

func serviceTestPolicy() Policy {
	return Policy{
		ForbiddenCorridorMeters:          5,
		ForcedStopRadiusMeters:           20,
		OptionalStopRadiusMeters:         500,
		SearchRadiusMeters:               1000,
		MaximumCutPositions:              100,
		MaximumCutLengthMeters:           20_000,
		MaximumTrafficTiles:              32,
		TrafficZoom:                      14,
		TrafficMatchRadiusMeters:         15,
		TrafficDirectionToleranceDegrees: 45,
		TrafficEstimateRadiusMeters:      300,
		TrafficEstimateMinimumSamples:    3,
		TrafficEstimateMaximumSamples:    5,
		PointDirectionToleranceDegrees:   60,
	}
}

func straightLine(from, to route.Position) *route.LineString {
	return &route.LineString{Positions: []route.Position{from, to}}
}

func positionOn(input route.Route, segment int, fraction float64) route.Position {
	from := input.Stops[segment].Position
	to := input.Stops[segment+1].Position
	return route.Position{
		Latitude:  from.Latitude + fraction*(to.Latitude-from.Latitude),
		Longitude: from.Longitude + fraction*(to.Longitude-from.Longitude),
	}
}
