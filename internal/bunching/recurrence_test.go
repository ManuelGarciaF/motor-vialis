package bunching

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type recurrenceRepository struct {
	current    func(context.Context, int64, time.Time, Policy) (Current, error)
	recurrence func(context.Context, *int64, time.Time, Policy, RecurrencePolicy) (RecurrencePage, error)
}

func (r recurrenceRepository) FindCurrent(ctx context.Context, id int64, now time.Time, p Policy) (Current, error) {
	return r.current(ctx, id, now, p)
}
func (r recurrenceRepository) FindHistory(context.Context, HistoryRequest, time.Time, Policy) (EventPage, error) {
	panic("unexpected history query")
}
func (r recurrenceRepository) FindRecurrence(ctx context.Context, id *int64, now time.Time, p Policy, rp RecurrencePolicy) (RecurrencePage, error) {
	return r.recurrence(ctx, id, now, p, rp)
}

func TestRecurrencePolicy(t *testing.T) {
	valid := RecurrencePolicy{Window: 7 * 24 * time.Hour, MinimumDays: 2, TimeZone: "America/Argentina/Buenos_Aires"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []RecurrencePolicy{{Window: 0, MinimumDays: 2, TimeZone: valid.TimeZone}, {Window: valid.Window, MinimumDays: 1, TimeZone: valid.TimeZone}, {Window: valid.Window, MinimumDays: 2, TimeZone: "invalid"}, {Window: valid.Window, MinimumDays: 2}} {
		if p.Validate() == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
}
func TestRecurrenceService(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	policy := Policy{DistanceMeters: 200, MaximumAge: 5 * time.Minute, MaximumFutureSkew: time.Minute, MinimumObservations: 2, MinimumDuration: 15 * time.Minute}
	rp := RecurrencePolicy{Window: 7 * 24 * time.Hour, MinimumDays: 2, TimeZone: "America/Argentina/Buenos_Aires"}
	summary := RecurrenceSummary{From: now.Add(-rp.Window), To: now, TimeZone: rp.TimeZone, MinimumDays: 2, EventCount: 3, DaysWithEvents: 2, Dates: []string{"2026-10-08", "2026-10-09"}, Recurrent: true, Hours: []HourRecurrence{{Hour: 8, EventCount: 3, DaysWithEvents: 2, Dates: []string{"2026-10-08", "2026-10-09"}, Recurrent: true}}}
	failure := errors.New("repository failed")
	var currentErr, recurrenceErr error
	calls := 0
	global := false
	repo := recurrenceRepository{
		current: func(_ context.Context, id int64, at time.Time, p Policy) (Current, error) {
			if id != 7 || !at.Equal(now) || p != policy {
				t.Fatal("wrong current request")
			}
			return Current{LineID: id, Stale: true, Partial: true, Pairs: []ActivePair{}}, currentErr
		},
		recurrence: func(_ context.Context, id *int64, at time.Time, p Policy, r RecurrencePolicy) (RecurrencePage, error) {
			calls++
			if !at.Equal(now) || p != policy || r != rp || (global && id != nil) || (!global && (id == nil || *id != 7)) {
				t.Fatal("wrong recurrence request")
			}
			return RecurrencePage{Lines: []LineRecurrence{{RecurrenceSummary: summary}}}, recurrenceErr
		},
	}
	s, err := NewService(repo, policy, HistoryPolicy{DefaultWindow: 24 * time.Hour, MaximumWindow: 30 * 24 * time.Hour, DefaultLimit: 50, MaximumLimit: 200}, rp)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	current, err := s.Get(context.Background(), 7)
	if err != nil || !reflect.DeepEqual(current.Recurrence, summary) || !current.Stale || !current.Partial {
		t.Fatalf("current: %+v %v", current, err)
	}
	currentErr = failure
	if _, err := s.Get(context.Background(), 7); !errors.Is(err, failure) || calls != 1 {
		t.Fatal("queried recurrence after current failure")
	}
	currentErr = nil
	recurrenceErr = failure
	if _, err := s.Get(context.Background(), 7); !errors.Is(err, failure) {
		t.Fatal("lost recurrence error")
	}
	global = true
	if _, err := s.Recurrence(context.Background()); !errors.Is(err, failure) {
		t.Fatal("lost global recurrence error")
	}
	recurrenceErr = nil
	page, err := s.Recurrence(context.Background())
	if err != nil || len(page.Lines) != 1 || !page.Lines[0].Recurrent {
		t.Fatalf("page: %+v %v", page, err)
	}
}
