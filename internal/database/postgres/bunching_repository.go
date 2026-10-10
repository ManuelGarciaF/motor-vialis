package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BunchingRepository struct {
	database realtimeBeginner
	schema   string
}
type readOnlyRealtimeDatabase struct{ pool *pgxpool.Pool }

func (d readOnlyRealtimeDatabase) Begin(ctx context.Context) (pgx.Tx, error) {
	return d.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
}
func NewBunchingRepository(pool *pgxpool.Pool) *BunchingRepository {
	return &BunchingRepository{database: readOnlyRealtimeDatabase{pool}, schema: "vialis"}
}

var _ bunching.CurrentRepository = (*BunchingRepository)(nil)

func (r *BunchingRepository) FindCurrent(ctx context.Context, id int64, now time.Time, policy bunching.Policy) (bunching.Current, error) {
	result := bunching.Current{LineID: id, Stale: true, Partial: true, Pairs: []bunching.ActivePair{}}
	tx, err := r.database.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())
	// Consistent read of line metadata, capture flags and event state in one
	// statement would be possible too; use a read-only repeatable-read tx in API.
	// Begin() supports nested transactions in integration tests.
	sql := (&RealtimeRepository{schema: r.schema}).sql
	var route, hash string
	err = tx.QueryRow(ctx, sql(findBunchingLineSQL), id).Scan(&route, &result.Direction, &result.Line, &result.Branch, &hash, &result.Mapped)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, lines.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	err = tx.QueryRow(ctx, sql(findLatestBunchingCaptureSQL)).Scan(&result.LatestCaptureAt, &result.Partial)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, tx.Commit(ctx)
	}
	if err != nil {
		return result, err
	}
	age := now.Sub(*result.LatestCaptureAt).Seconds()
	if age < 0 {
		age = 0
	}
	result.CaptureAgeSeconds = &age
	result.Stale = now.Sub(*result.LatestCaptureAt) > policy.MaximumAge || result.LatestCaptureAt.After(now.Add(policy.MaximumFutureSkew))
	if result.Partial || result.Stale || !result.Mapped {
		return result, tx.Commit(ctx)
	}
	rows, err := tx.Query(ctx, sql(findCurrentBunchingSQL),
		route, result.Direction, hash, policy.MinimumObservations,
		now.Add(-policy.MaximumAge), now.Add(policy.MaximumFutureSkew),
		policy.MinimumDuration.Seconds(), policy.MinimumSpeedMetersPerSecond,
	)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			rows.Close()
			return result, err
		}
		result.Pairs = append(result.Pairs, bunching.ActivePair{Event: event, RepresentativePoint: bunching.Midpoint(event.PointA, event.PointB), DurationSeconds: event.LastObservedAt.Sub(event.FirstObservedAt).Seconds(), ConfirmationPending: event.MissingCaptures > 0})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}
