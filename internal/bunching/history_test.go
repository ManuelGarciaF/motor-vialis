package bunching

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeHistory(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	policy := HistoryPolicy{DefaultWindow: 24 * time.Hour, MaximumWindow: 30 * 24 * time.Hour, DefaultLimit: 50, MaximumLimit: 200}
	r, err := NormalizeHistory(HistoryQuery{}, now, policy, time.Minute)
	if err != nil || r.Limit != 50 || r.State != "all" || !r.To.Equal(now) || !r.From.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("default: %+v %v", r, err)
	}
	future := now.Add(2 * time.Minute)
	equal := now
	old := now.Add(-31 * 24 * time.Hour)
	for _, test := range []struct {
		q     HistoryQuery
		field string
	}{
		{HistoryQuery{From: &equal}, "from"},
		{HistoryQuery{From: &old}, "from"},
		{HistoryQuery{To: &future}, "to"},
		{HistoryQuery{State: "active"}, "state"},
		{HistoryQuery{Limit: 201}, "limit"},
		{HistoryQuery{Offset: -1}, "offset"},
	} {
		_, err := NormalizeHistory(test.q, now, policy, time.Minute)
		var validation *QueryError
		if !errors.As(err, &validation) || validation.Field != test.field {
			t.Fatalf("query=%+v error=%v", test.q, err)
		}
	}
}
