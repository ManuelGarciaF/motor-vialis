package detour

import (
	"context"
	"errors"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
)

// coverageGapRouting connects the topology but returns no priced path while
// traffic observations are present, reproducing traffic_coverage_insufficient.
func coverageGapRouting(request RoutingRequest) RoutingResult {
	if len(request.Traffic) != 0 {
		return RoutingResult{TopologyAvailablePairs: request.Pairs}
	}
	return fullyConnectedRouting(request)
}

func observedTraffic() *scriptedTrafficProvider {
	return &scriptedTrafficProvider{snapshot: traffic.Snapshot{
		Segments:  []traffic.Segment{{HasSpeed: true, SpeedKPH: 30}},
		TileCount: 4,
		CacheHits: 2,
	}}
}

func TestTrafficFallbackRecoversInsufficientCoverage(t *testing.T) {
	for _, demo := range []bool{false, true} {
		input := serviceTestInput()
		input.Criterion = CriterionShortestTime
		repo := &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeResult: coverageGapRouting}
		provider := observedTraffic()
		s, err := NewService(repo, provider, &recordingComparator{}, serviceTestPolicy())
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.WithDemoTrafficFallback(demo).Plan(context.Background(), input)
		if err != nil {
			t.Fatalf("demo=%v: %v", demo, err)
		}
		if repo.routeCalls != 2 || provider.calls != 1 {
			t.Fatalf("demo=%v: route calls = %d, provider calls = %d", demo, repo.routeCalls, provider.calls)
		}
		last := repo.requests[1]
		if len(last.Traffic) != 0 || last.GraphMarginMeters != detourRetryGraphMarginMeters {
			t.Fatalf("demo=%v: fallback request traffic = %v, margin = %v", demo, last.Traffic, last.GraphMarginMeters)
		}
		trace := got.Trace.Traffic
		if !trace.Fallback || !trace.FetchedAt.IsZero() || trace.TrafficAge != 0 || trace.TileCount != 0 || trace.CacheHits != 0 {
			t.Fatalf("demo=%v: trace = %+v", demo, trace)
		}
	}
}

func TestTrafficFallbackRecoversProviderErrorOutsideDemo(t *testing.T) {
	codes := []traffic.ErrorCode{traffic.ErrorUnavailable, traffic.ErrorStale, traffic.ErrorInvalidTile, traffic.ErrorAPIKeyMissing}
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			input := serviceTestInput()
			input.Criterion = CriterionShortestTime
			repo := &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeResult: fullyConnectedRouting}
			provider := &scriptedTrafficProvider{err: &traffic.Error{Code: code}}
			s, err := NewService(repo, provider, &recordingComparator{}, serviceTestPolicy())
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Plan(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Trace.Traffic.Fallback || repo.routeCalls != 1 || provider.calls != 1 {
				t.Fatalf("trace = %+v, route calls = %d, provider calls = %d", got.Trace.Traffic, repo.routeCalls, provider.calls)
			}
			if len(repo.requests[0].Traffic) != 0 {
				t.Fatal("fallback must route without traffic observations")
			}
		})
	}
}

func TestTrafficFallbackSkipsNonQualifyingErrors(t *testing.T) {
	database := errors.New("database failed")
	testCases := []struct {
		name          string
		criterion     Criterion
		analyzeErr    error
		routeErr      error
		providerErr   error
		wantAnalyze   int
		wantRouteCall int
	}{
		{name: "validation error", criterion: Criterion("fastest"), wantAnalyze: 0},
		{name: "route not affected", analyzeErr: &Error{Code: ErrorRouteNotAffected, Kind: ErrorKindInvalidInput}, wantAnalyze: 1},
		{name: "cut not matched", analyzeErr: &Error{Code: ErrorCutNotMatched, Kind: ErrorKindInvalidInput}, wantAnalyze: 1},
		{name: "tile limit", providerErr: &traffic.Error{Code: traffic.ErrorTileLimit}, wantAnalyze: 1},
		{name: "database", routeErr: database, wantAnalyze: 1, wantRouteCall: 1},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			input := serviceTestInput()
			input.Criterion = CriterionShortestTime
			if testCase.criterion != "" {
				input.Criterion = testCase.criterion
			}
			repo := &scriptedDetourRepository{
				analysis:    serviceTestAnalysis(),
				analyzeErr:  testCase.analyzeErr,
				routeErr:    testCase.routeErr,
				routeResult: fullyConnectedRouting,
			}
			s, err := NewService(repo, &scriptedTrafficProvider{err: testCase.providerErr}, &recordingComparator{}, serviceTestPolicy())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Plan(context.Background(), input); err == nil {
				t.Fatal("expected error")
			}
			if repo.analyzeCalls != testCase.wantAnalyze || repo.routeCalls != testCase.wantRouteCall {
				t.Fatalf("analyze calls = %d, route calls = %d", repo.analyzeCalls, repo.routeCalls)
			}
		})
	}
}

func TestTrafficFallbackFailureReturnsOriginalError(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime
	repo := &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeResult: func(r RoutingRequest) RoutingResult {
		if len(r.Traffic) != 0 {
			return RoutingResult{TopologyAvailablePairs: r.Pairs}
		}
		// Without traffic there is no topology either: a different failure.
		return RoutingResult{}
	}}
	s, err := NewService(repo, observedTraffic(), &recordingComparator{}, serviceTestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Plan(context.Background(), input)
	var e *Error
	if !errors.As(err, &e) || e.Code != ErrorTrafficCoverageInsufficient {
		t.Fatalf("error = %v, want original traffic_coverage_insufficient", err)
	}
	if repo.routeCalls != 2 {
		t.Fatalf("route calls = %d", repo.routeCalls)
	}
}

func TestTrafficFallbackNeverRetriesCancellation(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeResult: fullyConnectedRouting}
	s, err := NewService(repo, &scriptedTrafficProvider{err: context.Canceled}, &recordingComparator{}, serviceTestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Plan(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("provider cancellation = %v", err)
	}
	if repo.routeCalls != 0 {
		t.Fatal("routed after provider cancellation")
	}

	// A qualifying error after the request was cancelled must not retry either.
	ctx, cancel = context.WithCancel(context.Background())
	repo = &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeResult: func(r RoutingRequest) RoutingResult {
		cancel()
		return coverageGapRouting(r)
	}}
	s, err = NewService(repo, observedTraffic(), &recordingComparator{}, serviceTestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Plan(ctx, input)
	var e *Error
	if !errors.As(err, &e) || e.Code != ErrorTrafficCoverageInsufficient {
		t.Fatalf("error = %v", err)
	}
	if repo.routeCalls != 1 {
		t.Fatalf("route calls after cancellation = %d", repo.routeCalls)
	}
}

func TestSuccessfulPlanDoesNotUseTrafficFallback(t *testing.T) {
	input := serviceTestInput()
	input.Criterion = CriterionShortestTime
	repo := &scriptedDetourRepository{analysis: serviceTestAnalysis(), routeResult: fullyConnectedRouting}
	provider := observedTraffic()
	s, err := NewService(repo, provider, &recordingComparator{}, serviceTestPolicy())
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Plan(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Trace.Traffic.Fallback || repo.routeCalls != 1 || provider.calls != 1 {
		t.Fatalf("trace = %+v, route calls = %d", got.Trace.Traffic, repo.routeCalls)
	}
	if got.Trace.Traffic.TileCount != 4 || len(repo.requests[0].Traffic) != 1 {
		t.Fatalf("observed snapshot not used: %+v", got.Trace.Traffic)
	}
}
