package bottlenecks

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBatchGuardsManifestAndEmission(t *testing.T) {
	repo := &analysisRepository{data: Dataset{LineID: 1, Version: testVersion(), LengthMeters: 1500, Observations: []CandidateObservation{candidates(0, 250), candidates(120, 1250)}}}
	req := AnalysisRequest{Window: Window{From: epoch, To: epoch.Add(time.Hour)}, NominalMeters: 500, Policy: testPolicy(), TimeZone: "America/Argentina/Buenos_Aires"}
	target := AnalysisTarget{LineID: 1, Version: testVersion(), ExpectedRawPositions: 2}
	emitted := 0
	sum, err := AnalyzeTargets(context.Background(), repo, []AnalysisTarget{target}, req, func(a Analysis) error { emitted++; return nil })
	if err != nil || emitted != 1 || sum.Targets != 1 || sum.ValidPassages != 1 || sum.IncompleteMeasurements != 1 {
		t.Fatalf("batch %+v %v", sum, err)
	}
	target.ExpectedRawPositions = 3
	if _, err := AnalyzeTargets(context.Background(), repo, []AnalysisTarget{target}, req, func(a Analysis) error { t.Fatal("emitted changed population"); return nil }); err == nil {
		t.Fatal("population changed silently")
	}
	target.ExpectedRawPositions = 2
	target.Version.ShapeHash = "changed"
	if _, err := AnalyzeTargets(context.Background(), repo, []AnalysisTarget{target}, req, func(a Analysis) error { t.Fatal("emitted changed geometry"); return nil }); err == nil {
		t.Fatal("geometry changed silently")
	}
	target.Version = testVersion()
	failure := errors.New("write failed")
	if _, err := AnalyzeTargets(context.Background(), repo, []AnalysisTarget{target}, req, func(a Analysis) error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("output failure %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AnalyzeTargets(ctx, repo, []AnalysisTarget{target}, req, func(a Analysis) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation %v", err)
	}
}
