package httpapi_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/httpapi"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
)

type fakeBunching struct {
	get        func(context.Context, int64) (bunching.Current, error)
	list       func(context.Context, bunching.HistoryQuery) (bunching.EventPage, error)
	recurrence func(context.Context) (bunching.RecurrencePage, error)
}

func (f fakeBunching) Recurrence(ctx context.Context) (bunching.RecurrencePage, error) {
	return f.recurrence(ctx)
}

func (f fakeBunching) List(ctx context.Context, q bunching.HistoryQuery) (bunching.EventPage, error) {
	return f.list(ctx, q)
}

func (f fakeBunching) Get(ctx context.Context, id int64) (bunching.Current, error) {
	return f.get(ctx, id)
}

func TestBunchingHTTP(t *testing.T) {
	for _, test := range []struct {
		name, path string
		err        error
		status     int
	}{
		{"current", "/lines/1/bunching", nil, 200},
		{"invalid id", "/lines/0/bunching", nil, 400},
		{"text id", "/lines/abc/bunching", nil, 400},
		{"unknown filter", "/lines/1/bunching?color=red", nil, 400},
		{"missing line", "/lines/999/bunching", lines.ErrNotFound, 404},
		{"deadline", "/lines/1/bunching", context.DeadlineExceeded, 504},
		{"repository failure", "/lines/1/bunching", errors.New("database failure"), 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			service := fakeBunching{get: func(ctx context.Context, id int64) (bunching.Current, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("no backend timeout")
				}
				return bunching.Current{LineID: id, Stale: true, Partial: true, Pairs: []bunching.ActivePair{}, Recurrence: bunching.RecurrenceSummary{EventCount: 3, DaysWithEvents: 2, Recurrent: true, Dates: []string{"2026-10-08", "2026-10-09"}, Hours: []bunching.HourRecurrence{{Hour: 8, EventCount: 3, DaysWithEvents: 2, Dates: []string{"2026-10-08", "2026-10-09"}, Recurrent: true}}}}, test.err
			}}
			h := httpapi.NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, nil, nil, nil, nil, time.Second, service)
			w := httptest.NewRecorder()
			h.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, test.path, nil))
			if w.Code != test.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if test.status == 400 && calls != 0 {
				t.Fatal("called backend on bad input")
			}
			if test.status == 200 {
				for _, field := range []string{`"stale":true`, `"partial":true`, `"pairs":[]`, `"recurrence":`, `"recurrent":true`, `"daysWithEvents":2`, `"hours":[`, `"hour":8`} {
					if !strings.Contains(w.Body.String(), field) {
						t.Fatal(w.Body.String())
					}
				}

				if strings.Contains(w.Body.String(), "severity") || strings.Contains(w.Body.String(), "color") {
					t.Fatal("invented severity")
				}
			}
		})
	}
}

func TestGlobalBunchingHTTP(t *testing.T) {
	for _, test := range []struct {
		path   string
		status int
		err    error
	}{
		{"/bunching", 200, nil},
		{"/bunching?from=2026-09-30T20:00:00Z&to=2026-10-01T00:00:00Z&state=closed&limit=20&offset=5", 200, nil},
		{"/bunching?from=bad", 400, nil},
		{"/bunching?state=open&state=closed", 400, nil},
		{"/bunching?unsupported=1", 400, nil},
		{"/bunching?limit=-1", 400, nil},
		{"/bunching?limit=0", 400, nil},
		{"/bunching?state=wrong", 400, &bunching.QueryError{Field: "state", Message: "invalid state"}},
		{"/bunching", 500, errors.New("query failed")},
	} {
		t.Run(test.path, func(t *testing.T) {
			service := fakeBunching{list: func(ctx context.Context, q bunching.HistoryQuery) (bunching.EventPage, error) {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("no timeout")
				}
				if q.State == "closed" && (q.From == nil || q.To == nil || q.Limit != 20 || q.Offset != 5) {
					t.Fatalf("query: %+v", q)
				}
				return bunching.EventPage{Events: []bunching.HistoricalEvent{}}, test.err
			}}
			h := httpapi.NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, nil, nil, nil, nil, time.Second, service)
			w := httptest.NewRecorder()
			h.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, test.path, nil))
			if w.Code != test.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if w.Code == 200 && !strings.Contains(w.Body.String(), `"events":[]`) {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestBunchingNotConfigured(t *testing.T) {
	h := httpapi.NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, nil, nil, nil, nil, time.Second)
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/lines/1/bunching", nil))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}
