package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func TestDemoBottlenecksDefaults(t *testing.T) {
	at := time.Date(2020, 1, 2, 3, 4, 5, 123000, time.UTC)
	for _, tc := range []struct {
		query    string
		code     int
		explicit bool
	}{
		{"", 200, false},
		{"line=132", 200, false},
		{detectionPeriod, 200, true},
		{"from=2020-01-01T00:00:00Z", 400, false},
		{"to=2020-01-01T00:00:00Z", 400, false},
		{"from=&to=", 400, false},
		{"to=&to=", 400, false},
		{"limit=1", 400, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			calls := 0
			h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, time.Second).
				WithDemoTime(at).WithBottlenecks(fakeBottlenecks(func(_ context.Context, q bottlenecks.DetectionQuery) (bottlenecks.DetectionPage, error) {
				calls++
				wantTo := at
				wantFrom := at.Add(-24 * time.Hour)
				if tc.explicit {
					wantFrom, _ = time.Parse(time.RFC3339, "2026-10-21T18:00:00-03:00")
					wantTo, _ = time.Parse(time.RFC3339, "2026-10-21T20:00:00-03:00")
				}
				if !q.From.Equal(wantFrom) || !q.To.Equal(wantTo) {
					t.Fatalf("unexpected range: %+v", q)
				}
				return bottlenecks.DetectionPage{From: q.From, To: q.To, DetectionData: bottlenecks.DetectionData{Sectors: []bottlenecks.DetectedSector{}}}, nil
			}))
			w := httptest.NewRecorder()
			h.Routes().ServeHTTP(w, httptest.NewRequest("GET", "/bottlenecks?"+tc.query, nil))
			if w.Code != tc.code {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("X-Vialis-Demo-Time") != at.Format(time.RFC3339Nano) {
				t.Fatal("missing demo time")
			}
			if (calls == 1) != (tc.code == 200) {
				t.Fatalf("calls = %d", calls)
			}
		})
	}
}

func TestLiveModeHasNoDemoHeader(t *testing.T) {
	h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, time.Second)
	w := httptest.NewRecorder()
	h.Routes().ServeHTTP(w, httptest.NewRequest("GET", "/bottlenecks", nil))
	if w.Code != 400 || w.Header().Get("X-Vialis-Demo-Time") != "" {
		t.Fatalf("changed live behavior: %+v", w.Result())
	}
}
