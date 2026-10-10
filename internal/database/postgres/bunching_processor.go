package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/jackc/pgx/v5"
)

// EnableBunching must be called before the repository is shared with a worker.
func (r *RealtimeRepository) EnableBunching(policy bunching.Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	r.bunchingPolicy = &policy
	return nil
}

type eventScanner interface{ Scan(...any) error }

func scanEvent(row eventScanner, extra ...any) (bunching.Event, error) {
	var e bunching.Event
	// Keep this order aligned with the explicit event projections in find_*_bunching.sql.
	args := []any{
		&e.ID,
		&e.Scope.GCBARouteID, &e.Scope.GCBADirection,
		&e.Scope.GTFSRouteID, &e.Scope.GTFSDirection, &e.Scope.ShapeID, &e.Scope.ShapeHash,
		&e.VehicleA, &e.VehicleB,
		&e.FirstObservedAt, &e.LastObservedAt, &e.ObservedA, &e.ObservedB,
		&e.ClosedAt, &e.CloseReason,
		&e.MinimumDistanceMeters, &e.LastDistanceMeters,
		&e.PointA.Longitude, &e.PointA.Latitude, &e.PointB.Longitude, &e.PointB.Latitude,
		&e.Observations, &e.MissingCaptures, &e.Line, &e.Branch, &e.ConfirmedAt,
		&e.MaximumSpeedAMetersPerSecond, &e.MaximumSpeedBMetersPerSecond,
	}
	err := row.Scan(append(args, extra...)...)
	return e, err
}

func (r *RealtimeRepository) advanceBunching(ctx context.Context, tx pgx.Tx, capture bunching.Capture, result *bunching.StoreResult) error {
	policy := *r.bunchingPolicy
	var latest *time.Time
	if err := tx.QueryRow(ctx, r.sql(`SELECT max(recibido_en) FROM vialis.capturas_colectivos WHERE aglutinaciones_analizadas`)).Scan(&latest); err != nil {
		return err
	}
	// Late/replayed captures cannot move the operational clock backwards.
	if latest != nil && !capture.ReceivedAt.After(*latest) {
		result.BunchingSkipped = true
		return nil
	}
	result.BunchingSkipped = capture.Partial
	fingerprint := bunching.Fingerprint(capture.Positions)
	if !capture.Partial {
		var previousFingerprint string
		err := tx.QueryRow(ctx, r.sql(`SELECT hash_posiciones FROM vialis.capturas_colectivos WHERE aglutinaciones_analizadas AND NOT cobertura_parcial ORDER BY recibido_en DESC,id_captura DESC LIMIT 1`)).Scan(&previousFingerprint)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(ctx, r.sql(createBunchingSnapshotSQL), capture.ReceivedAt.Add(-policy.MaximumAge), capture.ReceivedAt.Add(policy.MaximumFutureSkew))
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, prepareBunchingSnapshotSQL); err != nil {
			return err
		}
		snapshot := bunching.Snapshot{ReceivedAt: capture.ReceivedAt, HasNewPositions: fingerprint != previousFingerprint, Positions: map[string]bunching.Observation{}, Distances: map[bunching.PairKey]float64{}}
		rows, err := tx.Query(ctx, findBunchingSnapshotPositionsSQL)
		if err != nil {
			return err
		}
		for rows.Next() {
			var o bunching.Observation
			if err = rows.Scan(&o.VehicleID, &o.ObservedAt, &o.Scope.GCBARouteID, &o.Scope.GCBADirection, &o.Scope.GTFSRouteID, &o.Scope.GTFSDirection, &o.Scope.ShapeID, &o.Scope.ShapeHash, &o.Point.Longitude, &o.Point.Latitude, &o.Line, &o.Branch, &o.SpeedMetersPerSecond); err != nil {
				rows.Close()
				return err
			}
			snapshot.Positions[o.VehicleID] = o
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, r.sql(findOpenBunchingSQL))
		if err != nil {
			return err
		}
		var open []bunching.Event
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				rows.Close()
				return err
			}
			open = append(open, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		// Expand only the index prefilter by a micrometre: ST_Project/ST_DWithin
		// can disagree at exactly 200m due to inverse-geodesic precision. The
		// domain still applies the strict <=200m ST_Distance result.
		rows, err = tx.Query(ctx, r.sql(findBunchingPairDistancesSQL), policy.DistanceMeters+0.000001)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key bunching.PairKey
			var distance float64
			if err = rows.Scan(&key.Scope.GCBARouteID, &key.Scope.GCBADirection, &key.Scope.GTFSRouteID, &key.Scope.GTFSDirection, &key.Scope.ShapeID, &key.Scope.ShapeHash, &key.A, &key.B, &distance); err != nil {
				rows.Close()
				return err
			}
			snapshot.Distances[key] = distance
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if snapshot.HasNewPositions {
			snapshot.Previous = make(map[bunching.PairKey]bunching.PreviousSample)
			rows, err = tx.Query(ctx, r.sql(findRecentClosedBunchingSQL), capture.ReceivedAt.Add(-policy.MaximumAge))
			if err != nil {
				return err
			}
			for rows.Next() {
				e, scanErr := scanEvent(rows)
				if scanErr != nil {
					rows.Close()
					return scanErr
				}
				key := bunching.Key(e.Scope, e.VehicleA, e.VehicleB)
				if _, exists := snapshot.Previous[key]; !exists {
					snapshot.Previous[key] = bunching.PreviousSample{ObservedA: e.ObservedA, ObservedB: e.ObservedB}
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		changes, err := bunching.Advance(open, snapshot, policy)
		if err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for _, event := range changes {
			queueEvent(batch, r, event)
			if event.ID == 0 {
				result.EventsOpened++
			} else if event.ClosedAt != nil {
				result.EventsClosed++
			} else {
				result.EventsUpdated++
			}
		}
		results := tx.SendBatch(ctx, batch)
		if err = results.Close(); err != nil {
			return fmt.Errorf("persist bunching changes: %w", err)
		}
		if _, err = tx.Exec(ctx, `DROP TABLE bunching_snapshot`); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, r.sql(`UPDATE vialis.capturas_colectivos SET aglutinaciones_analizadas=TRUE,hash_posiciones=$2 WHERE id_captura=$1`), result.CaptureID, fingerprint)
	result.BunchingProcessed = err == nil
	return err
}

func queueEvent(batch *pgx.Batch, r *RealtimeRepository, e bunching.Event) {
	args := []any{
		e.Scope.GCBARouteID, e.Scope.GCBADirection,
		e.Scope.GTFSRouteID, e.Scope.GTFSDirection, e.Scope.ShapeID, e.Scope.ShapeHash,
		e.VehicleA, e.VehicleB,
		e.FirstObservedAt, e.LastObservedAt, e.ObservedA, e.ObservedB,
		e.ClosedAt, e.CloseReason,
		e.MinimumDistanceMeters, e.LastDistanceMeters,
		e.PointA.Longitude, e.PointA.Latitude, e.PointB.Longitude, e.PointB.Latitude,
		e.Observations, e.MissingCaptures, e.Line, e.Branch, e.ConfirmedAt,
		e.MaximumSpeedAMetersPerSecond, e.MaximumSpeedBMetersPerSecond,
	}
	// Empty closure reasons are SQL NULL, not an invalid enum value.
	if e.CloseReason == "" {
		args[13] = nil
	}
	if e.ID == 0 {
		batch.Queue(r.sql(insertBunchingEventSQL), args...)
	} else {
		args = append(args, e.ID)
		batch.Queue(r.sql(updateBunchingEventSQL), args...)
	}
}
