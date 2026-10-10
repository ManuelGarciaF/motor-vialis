package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/httpapi"
)

func TestBunchingRecurrenceHTTP(t *testing.T) {
	for _, tc := range []struct {
		path       string
		err        error
		configured bool
		status     int
	}{
		{"/bunching/recurrence", nil, true, 200},
		{"/bunching/recurrence?limit=10", nil, true, 400},
		{"/bunching/recurrence", errors.New("private database failure"), true, 500},
		{"/bunching/recurrence", context.DeadlineExceeded, true, 504},
		{"/bunching/recurrence", nil, false, 503},
	} {
		t.Run(tc.path, func(t *testing.T) {
			calls := 0
			service := fakeBunching{recurrence: func(ctx context.Context) (bunching.RecurrencePage, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("no timeout")
				}
				return bunching.RecurrencePage{Lines: []bunching.LineRecurrence{{GTFSRouteID: "route", RecurrenceSummary: bunching.RecurrenceSummary{Recurrent: true, EventCount: 3, DaysWithEvents: 2, Dates: []string{"2026-10-08", "2026-10-09"}, Hours: []bunching.HourRecurrence{{Hour: 8, EventCount: 3, DaysWithEvents: 2, Dates: []string{"2026-10-08", "2026-10-09"}, Recurrent: true}}}}}}, tc.err
			}}
			var services []httpapi.Bunching
			if tc.configured {
				services = append(services, service)
			}
			h := httpapi.NewHandler(slog.New(slog.NewJSONHandler(io.Discard, nil)), nil, nil, nil, nil, nil, time.Second, services...)
			w := httptest.NewRecorder()
			h.Routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tc.status == 400 && calls != 0 {
				t.Fatal("backend called for invalid query")
			}
			if tc.status == 200 {
				var page bunching.RecurrencePage
				if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if len(page.Lines) != 1 || !page.Lines[0].Recurrent || page.Lines[0].DaysWithEvents != 2 || len(page.Lines[0].Hours) != 1 || page.Lines[0].Hours[0].Hour != 8 || !page.Lines[0].Hours[0].Recurrent {
					t.Fatalf("page: %+v", page)
				}
			}
		})
	}
}
