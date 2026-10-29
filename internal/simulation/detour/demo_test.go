package detour

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
)

func TestDemoTrafficProviderErrorUsesExistingFallback(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime
	for _, code := range []traffic.ErrorCode{traffic.ErrorUnavailable, traffic.ErrorStale, traffic.ErrorInvalidTile, traffic.ErrorAPIKeyMissing} {
		t.Run(string(code), func(t *testing.T) {
			repo := &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeResult: func(r RoutingRequest) RoutingResult {
				if len(r.Traffic) != 0 {
					t.Fatal("provider error must supply no traffic observations")
				}
				return fullyConnectedRouting(r)
			}}
			s, err := NewService(repo, &scriptedTrafficProvider{err: &traffic.Error{Code: code}}, &recordingComparator{}, serviceTestPolicy())
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.WithDemoTrafficFallback(true).Plan(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Trace.Traffic.FetchedAt.IsZero() || got.Trace.Traffic.TileCount != 0 || got.Trace.Traffic.CacheHits != 0 {
				t.Fatalf("invented provider metadata: %+v", got.Trace.Traffic)
			}
			if repo.routeCalls != 1 {
				t.Fatalf("routing calls = %d", repo.routeCalls)
			}
		})
	}
}

func TestDemoPreservesSuccessfulTrafficSnapshot(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime
	for _, segments := range [][]traffic.Segment{nil, {{HasSpeed: true, SpeedKPH: 40}}, {{SourceID: "no-speed"}}} {
		repo := &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeResult: func(r RoutingRequest) RoutingResult {
			if !reflect.DeepEqual(r.Traffic, segments) {
				t.Fatalf("changed successful snapshot: %+v", r.Traffic)
			}
			return fullyConnectedRouting(r)
		}}
		s, err := NewService(repo, &scriptedTrafficProvider{snapshot: traffic.Snapshot{Segments: segments}}, &recordingComparator{}, serviceTestPolicy())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.WithDemoTrafficFallback(true).Plan(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDemoDoesNotRetryInsufficientCoverage(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime
	repo := &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeResult: func(r RoutingRequest) RoutingResult {
		return RoutingResult{TopologyAvailablePairs: r.Pairs}
	}}
	s, err := NewService(repo, &scriptedTrafficProvider{snapshot: traffic.Snapshot{Segments: []traffic.Segment{{HasSpeed: true, SpeedKPH: 30}}}}, &recordingComparator{}, serviceTestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.WithDemoTrafficFallback(true).Plan(context.Background(), input)
	var e *Error
	if !errors.As(err, &e) || e.Code != ErrorTrafficCoverageInsufficient {
		t.Fatalf("coverage error = %v", err)
	}
	if repo.routeCalls != 1 {
		t.Fatalf("unexpected coverage retry: %d", repo.routeCalls)
	}
}

func TestDemoDoesNotHideRoutingErrorsOrCancellation(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime
	failure := errors.New("database failed")
	repo := &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeErr: failure}
	s, err := NewService(repo, &scriptedTrafficProvider{}, &recordingComparator{}, serviceTestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	s.WithDemoTrafficFallback(true)
	if _, err := s.Plan(context.Background(), input); !errors.Is(err, failure) {
		t.Fatalf("database error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo.routeCalls = 0
	s.trafficProvider = &scriptedTrafficProvider{err: context.Canceled}
	if _, err := s.Plan(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	if repo.routeCalls != 0 {
		t.Fatal("routed after cancellation")
	}
}
