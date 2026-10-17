package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/jackc/pgx/v5"
)

type fakeCaptureRow struct {
	receivedAt time.Time
	partial    bool
	err        error
}

func (r fakeCaptureRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}

	*dest[0].(*time.Time) = r.receivedAt
	*dest[1].(*bool) = r.partial
	return nil
}

type fakeCaptureQueryer struct {
	t   *testing.T
	ctx context.Context
	row fakeCaptureRow
}

func (q fakeCaptureQueryer) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	q.t.Helper()
	if ctx != q.ctx || sql != queryInSchema(findLatestBunchingCaptureSQL, `test"schema`) || len(args) != 0 {
		q.t.Fatal("capture helper changed context, schema or arguments")
	}
	return q.row
}

func TestReadBunchingCaptureState(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	policy := bunching.Policy{MaximumAge: 5 * time.Minute, MaximumFutureSkew: time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	failure := errors.New("database failed")
	for _, tc := range []struct {
		name           string
		offset         time.Duration
		partial, stale bool
		err            error
	}{
		{name: "fresh", offset: -time.Minute},
		{name: "partial", offset: -time.Minute, partial: true},
		{name: "maximum age inclusive", offset: -5 * time.Minute},
		{name: "stale", offset: -5*time.Minute - time.Nanosecond, stale: true},
		{name: "future allowed", offset: time.Minute},
		{name: "future invalid", offset: time.Minute + time.Nanosecond, stale: true},
		{name: "missing", err: pgx.ErrNoRows, partial: true, stale: true},
		{name: "wrapped missing", err: fmt.Errorf("scan: %w", pgx.ErrNoRows), partial: true, stale: true},
		{name: "database error", err: failure},
		{name: "cancellation", err: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := now.Add(tc.offset)
			queryer := fakeCaptureQueryer{t: t, ctx: ctx, row: fakeCaptureRow{receivedAt: at, partial: tc.partial, err: tc.err}}
			state, err := readBunchingCaptureState(ctx, queryer, `test"schema`, now, policy)
			missing := errors.Is(tc.err, pgx.ErrNoRows)
			if tc.err != nil && !missing {
				if !errors.Is(err, tc.err) {
					t.Fatalf("lost query error: %v", err)
				}
				return
			}
			if err != nil || state.partial != tc.partial || state.stale != tc.stale {
				t.Fatalf("state=%+v err=%v", state, err)
			}
			if missing {
				if state.latestCaptureAt != nil || state.captureAgeSeconds != nil {
					t.Fatal("missing capture invented metadata")
				}
				return
			}

			age := -tc.offset.Seconds()
			if age < 0 {
				age = 0
			}
			if state.latestCaptureAt == nil || !state.latestCaptureAt.Equal(at) || state.captureAgeSeconds == nil || *state.captureAgeSeconds != age {
				t.Fatalf("wrong timestamps/age: %+v", state)
			}
		})
	}
}
