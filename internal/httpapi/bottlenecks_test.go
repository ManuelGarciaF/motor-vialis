package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

type fakeBottlenecks func(context.Context, bottlenecks.DetectionQuery) (bottlenecks.DetectionPage, error)

func (f fakeBottlenecks) List(ctx context.Context, q bottlenecks.DetectionQuery) (bottlenecks.DetectionPage, error) {
	return f(ctx, q)
}

const detectionPeriod = "from=2026-10-21T18:00:00-03:00&to=2026-10-21T20:00:00-03:00"

func TestBottlenecksEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, suffix string
		err          error
		unavailable  bool
		code         int
	}{
		{name: "ok", suffix: "&line=132&bbox=-58.6,-34.8,-58.2,-34.4", code: 200},
		{name: "internal", err: errors.New("secret database failure"), code: 500},
		{name: "timeout", err: context.DeadlineExceeded, code: 504},
		{name: "validation", err: &bottlenecks.DetectionQueryError{Field: "to", Message: "bad range"}, code: 400},
		{name: "unavailable", unavailable: true, code: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			fake := fakeBottlenecks(func(ctx context.Context, q bottlenecks.DetectionQuery) (bottlenecks.DetectionPage, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Error("missing timeout")
				}
				if tc.code == 200 && (q.Line != "132" || q.Bounds == nil) {
					t.Errorf("missing filters: %+v", q)
				}
				return bottlenecks.DetectionPage{DetectionData: bottlenecks.DetectionData{Sectors: []bottlenecks.DetectedSector{}}}, tc.err
			})
			h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, time.Second)
			if !tc.unavailable {
				h.WithBottlenecks(fake)
			}
			recorder := httptest.NewRecorder()
			h.Routes().ServeHTTP(recorder, httptest.NewRequest("GET", "/bottlenecks?"+detectionPeriod+tc.suffix, nil))
			if recorder.Code != tc.code {
				t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "secret") {
				t.Fatal("leaked error")
			}
			if tc.code == 200 {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(recorder.Body.Bytes(), &fields); err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"experimental", "source", "policy"} {
					if _, present := fields[field]; present {
						t.Fatalf("response must not expose %s field", field)
					}
				}
				var page bottlenecks.DetectionPage
				if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if page.Sectors == nil {
					t.Fatalf("bad page: %+v", page)
				}
			}
			if !tc.unavailable && calls != 1 {
				t.Fatal("reader not called")
			}
		})
	}
}
func TestBottlenecksQueryValidation(t *testing.T) {
	for _, query := range []string{
		"", "from=bad&to=bad", "from=2026-10-21&to=2026-10-22", "from=2026-10-22T00:00:00Z&to=2026-10-21T00:00:00Z",
		detectionPeriod + "&from=2026-10-21T00:00:00Z", detectionPeriod + "&limit=20", detectionPeriod + "&bbox=",
		detectionPeriod + "&bbox=NaN,0,1,1", detectionPeriod + "&bbox=-181,0,1,1", detectionPeriod + "&bbox=0,2,1,1",
		detectionPeriod + "&bbox=1,2", detectionPeriod + "&line=%zz",
	} {
		h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, nil, nil, time.Second).WithBottlenecks(fakeBottlenecks(func(context.Context, bottlenecks.DetectionQuery) (bottlenecks.DetectionPage, error) {
			t.Fatal("invalid query reached service")
			return bottlenecks.DetectionPage{}, nil
		}))
		w := httptest.NewRecorder()
		h.Routes().ServeHTTP(w, httptest.NewRequest("GET", "/bottlenecks?"+query, nil))
		if w.Code != 400 {
			t.Errorf("query %q: %d %s", query, w.Code, w.Body.String())
		}
	}
}
