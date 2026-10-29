package bunching_test

import (
	"context"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
)

type demoRepository struct {
	t     *testing.T
	at    time.Time
	calls int
}

func (r *demoRepository) check(at time.Time) {
	r.t.Helper()
	if !at.Equal(r.at) {
		r.t.Fatalf("clock = %v, want %v", at, r.at)
	}
	r.calls++
}
func (r *demoRepository) FindCurrent(_ context.Context, _ int64, at time.Time, _ bunching.Policy) (bunching.Current, error) {
	r.check(at)
	return bunching.Current{}, nil
}
func (r *demoRepository) FindHistory(_ context.Context, q bunching.HistoryRequest, at time.Time, _ bunching.Policy) (bunching.EventPage, error) {
	r.check(at)
	if !q.To.Equal(r.at) || !q.From.Equal(r.at.Add(-config.BunchingHistoryPolicy().DefaultWindow)) {
		r.t.Fatalf("history did not use fixed clock: %+v", q)
	}
	return bunching.EventPage{}, nil
}
func (r *demoRepository) FindRecurrence(_ context.Context, _ *int64, at time.Time, _ bunching.Policy, _ bunching.RecurrencePolicy) (bunching.RecurrencePage, error) {
	r.check(at)
	return bunching.RecurrencePage{Lines: []bunching.LineRecurrence{{}}}, nil
}
func TestDemoClockAllBunchingEndpoints(t *testing.T) {
	at := time.Date(2020, 1, 2, 3, 4, 5, 123000, time.UTC)
	repo := &demoRepository{t: t, at: at}
	s, err := bunching.NewService(repo, config.BunchingPolicy(), config.BunchingHistoryPolicy(), config.BunchingRecurrencePolicy())
	if err != nil {
		t.Fatal(err)
	}
	s.WithFixedTime(at)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := s.Get(ctx, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := s.List(ctx, bunching.HistoryQuery{}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Recurrence(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if repo.calls != 8 {
		t.Fatalf("calls = %d", repo.calls)
	}
}
