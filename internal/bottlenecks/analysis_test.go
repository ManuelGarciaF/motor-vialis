package bottlenecks

import (
	"context"
	"errors"
	"testing"
	"time"
)

type analysisRepository struct {
	data  Dataset
	err   error
	calls int
}

func (r *analysisRepository) Load(_ context.Context, id int64, w Window, radius float64) (Dataset, error) {
	r.calls++
	return r.data, r.err
}

func TestAnalysisReport(t *testing.T) {
	repo := &analysisRepository{data: Dataset{LineID: 1, Version: testVersion(), LengthMeters: 2000, Observations: []CandidateObservation{candidates(0, 250), candidates(120, 1250)}}}
	request := AnalysisRequest{LineID: 1, Window: Window{From: epoch, To: epoch.Add(time.Hour)}, NominalMeters: 500, Policy: testPolicy(), TimeZone: "America/Argentina/Buenos_Aires"}
	report, err := Analyze(context.Background(), repo, request)
	if err != nil {
		t.Fatal(err)
	}
	if report.RawPositions != 2 || report.SpatiallyUnique != 2 || report.Policy.MaximumGapSeconds != 180 {
		t.Fatalf("report %+v", report)
	}
	s := report.Sectors[1]
	if s.Passages != 1 ||
		s.SameIntervalPassages != 1 ||
		s.InteriorGPSPassages != 0 ||
		s.MedianDurationSeconds == nil ||
		*s.MedianDurationSeconds != 60 {
		t.Fatalf("sector %+v", s)
	}
	if report.Sectors[0].MedianDurationSeconds != nil || report.Sectors[0].TypicalSpeedKPH != nil {
		t.Fatal("no data is not zero speed")
	}
	request.LineID = 0
	if _, err := Analyze(context.Background(), repo, request); err == nil || repo.calls != 1 {
		t.Fatal("invalid request reached DB")
	}
	request.LineID = 1
	repo.err = ErrLineNotFound
	if _, err := Analyze(context.Background(), repo, request); !errors.Is(err, ErrLineNotFound) {
		t.Fatalf("lost not found: %v", err)
	}
}
