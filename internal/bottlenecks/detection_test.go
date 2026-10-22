package bottlenecks

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type detectionRepoFunc func(context.Context, string, DetectionQuery, DetectionPolicy) (DetectionData, error)

func (f detectionRepoFunc) FindDetectedSectors(ctx context.Context, m string, q DetectionQuery, p DetectionPolicy) (DetectionData, error) {
	return f(ctx, m, q, p)
}
func TestDetectionService(t *testing.T) {
	from := time.Date(2026, 10, 21, 18, 0, 0, 0, time.FixedZone("BA", -10800))
	p := DetectionPolicy{6, 2}
	calls := 0
	repository := detectionRepoFunc(func(ctx context.Context, m string, q DetectionQuery, policy DetectionPolicy) (DetectionData, error) {
		calls++
		if m != "operational" || policy != p || q.Line != "132" || q.From.Location() != time.UTC || q.To.Location() != time.UTC {
			t.Fatalf("unexpected repository request: %s %+v %+v", m, q, policy)
		}
		return DetectionData{Source: DetectionSource{Initialized: true, Checkpoint: 123}}, nil
	})
	s, err := NewDetectionService(repository, "operational", p)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.List(context.Background(), DetectionQuery{From: from, To: from.Add(time.Hour), Line: " 132 "})
	if err != nil {
		t.Fatal(err)
	}
	if !page.Experimental || page.Policy != p || page.Sectors == nil || page.Source.Checkpoint != 123 || !page.From.Equal(from) {
		t.Fatalf("unexpected page: %+v", page)
	}
	for _, q := range []DetectionQuery{
		{}, {From: from}, {From: from, To: from}, {From: from, To: from.Add(-time.Hour)},
		{From: from, To: from.Add(time.Hour), Bounds: &DetectionBounds{MinLongitude: math.NaN()}},
		{From: from, To: from.Add(time.Hour), Bounds: &DetectionBounds{MaxLongitude: math.Inf(1)}},
		{From: from, To: from.Add(time.Hour), Bounds: &DetectionBounds{MinLongitude: 10, MaxLongitude: 9}},
		{From: from, To: from.Add(time.Hour), Bounds: &DetectionBounds{MinLatitude: -91}},
	} {
		if _, err := s.List(context.Background(), q); err == nil {
			t.Errorf("accepted %+v", q)
		}
	}
	if calls != 1 {
		t.Fatalf("invalid queries reached repository: %d", calls)
	}
	sentinel := errors.New("storage failed")
	s.repository = detectionRepoFunc(func(context.Context, string, DetectionQuery, DetectionPolicy) (DetectionData, error) {
		return DetectionData{}, sentinel
	})
	if _, err := s.List(context.Background(), DetectionQuery{From: from, To: from.Add(time.Hour)}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}
func TestDetectionConfiguration(t *testing.T) {
	repo := detectionRepoFunc(func(context.Context, string, DetectionQuery, DetectionPolicy) (DetectionData, error) {
		return DetectionData{}, nil
	})
	for _, p := range []DetectionPolicy{{0, 2}, {6, 0}, {math.NaN(), 2}, {math.Inf(1), 2}} {
		if _, err := NewDetectionService(repo, "operational", p); err == nil {
			t.Errorf("accepted %+v", p)
		}
	}
	if _, err := NewDetectionService(nil, "operational", DetectionPolicy{6, 2}); err == nil {
		t.Fatal("nil repository")
	}
	if _, err := NewDetectionService(repo, " ", DetectionPolicy{6, 2}); err == nil {
		t.Fatal("empty model")
	}
}
