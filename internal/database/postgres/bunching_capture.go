package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/jackc/pgx/v5"
)

type captureQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type bunchingCaptureState struct {
	latestCaptureAt   *time.Time
	captureAgeSeconds *float64
	partial, stale    bool
}

// readBunchingCaptureState uses the caller's transaction, so metadata and event
// reads still share the same snapshot. No analysed capture means unknown
// coverage (partial/stale), not a complete capture containing zero encounters.
func readBunchingCaptureState(ctx context.Context, queryer captureQueryer, schema string, now time.Time, policy bunching.Policy) (bunchingCaptureState, error) {
	var receivedAt time.Time
	var partial bool
	err := queryer.QueryRow(ctx, queryInSchema(findLatestBunchingCaptureSQL, schema)).Scan(&receivedAt, &partial)
	if errors.Is(err, pgx.ErrNoRows) {
		return bunchingCaptureState{partial: true, stale: true}, nil
	}
	if err != nil {
		return bunchingCaptureState{}, err
	}

	age := now.Sub(receivedAt).Seconds()
	if age < 0 {
		age = 0
	}
	return bunchingCaptureState{
		latestCaptureAt:   &receivedAt,
		captureAgeSeconds: &age,
		partial:           partial,
		stale:             now.Sub(receivedAt) > policy.MaximumAge || receivedAt.After(now.Add(policy.MaximumFutureSkew)),
	}, nil
}
