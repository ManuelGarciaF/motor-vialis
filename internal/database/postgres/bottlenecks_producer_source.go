package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
)

type producerSourceBounds struct {
	first, last   int64
	vehicle       *string
	closedThrough *time.Time
}

// Layout lookup follows the identity saved in raw, not today's equivalences.
// An exact snapshot can survive catalog replacement; a missing version cannot
// be reconstructed by projecting old evidence onto a different geometry.
func (s *BottlenecksStore) ensureProducerLayouts(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, bounds producerSourceBounds, layouts map[int64]bottlenecks.PersistedLayout) error {
	rows, err := tx.Query(ctx, s.sql(listProducerRawVersionsSQL), bounds.first, bounds.last, bounds.vehicle, bounds.closedThrough)
	if err != nil {
		return err
	}
	var versions []bottlenecks.ShapeVersion
	for rows.Next() {
		var v bottlenecks.ShapeVersion
		if err = rows.Scan(&v.RouteID, &v.Direction, &v.ShapeID, &v.ShapeHash); err != nil {
			break
		}
		versions = append(versions, v)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowErr != nil {
		return rowErr
	}
	for _, v := range versions {
		var id int64
		err = tx.QueryRow(ctx, s.sql(findBottleneckLayoutIdentitySQL), run.Model.Name, v.RouteID, v.Direction, v.ShapeID, v.ShapeHash).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			var lineID int64
			err = tx.QueryRow(ctx, s.sql(findProducerCatalogVersionSQL), v.RouteID, v.Direction, v.ShapeID, v.ShapeHash).Scan(&lineID)
			if errors.Is(err, pgx.ErrNoRows) {
				return bottlenecks.ErrProducerGeometryUnavailable
			}
			if err != nil {
				return err
			}
			snapshot, err := s.readCatalogSnapshot(ctx, tx, run, lineID)
			if err != nil {
				return err
			}
			if snapshot.layout.Version() != v {
				return bottlenecks.ErrProducerGeometryUnavailable
			}
			persisted, err := s.persistLayoutSnapshot(ctx, tx, run, lineID, snapshot)
			if err != nil {
				return err
			}
			layouts[persisted.ID] = persisted
		} else if err != nil {
			return err
		} else if _, ok := layouts[id]; !ok {
			layout, err := s.readLayout(ctx, tx, run, id)
			if err != nil {
				return err
			}
			layouts[id] = layout
		}
	}
	return nil
}

func (s *BottlenecksStore) readProducerCandidates(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, bounds producerSourceBounds) ([]bottlenecks.ProducerObservation, error) {
	rows, err := tx.Query(ctx, s.sql(findProducerCandidatesSQL), run.Model.Name, bounds.first, bounds.last, run.Model.Policy.MaximumDistanceMeters, bounds.vehicle, bounds.closedThrough)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []bottlenecks.ProducerObservation{}
	for rows.Next() {
		var row bottlenecks.ProducerObservation
		candidate := &row.Candidate
		var progress, distances []float64
		if err = rows.Scan(&row.CaptureID, &row.LayoutID, &candidate.VehicleID, &candidate.Scope.ProviderRouteID, &candidate.Scope.ProviderDirection, &candidate.Scope.Version.RouteID, &candidate.Scope.Version.Direction, &candidate.Scope.Version.ShapeID, &candidate.Scope.Version.ShapeHash, &candidate.ObservedAt, &progress, &distances); err != nil {
			return nil, err
		}
		if len(progress) != len(distances) {
			return nil, fmt.Errorf("producer projection arrays differ")
		}
		for i, p := range progress {
			candidate.Candidates = append(candidate.Candidates, bottlenecks.Projection{ProgressMeters: p, DistanceMeters: distances[i]})
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func producerLayoutsSlice(layouts map[int64]bottlenecks.PersistedLayout) []bottlenecks.PersistedLayout {
	ids := make([]int64, 0, len(layouts))
	for id := range layouts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	result := make([]bottlenecks.PersistedLayout, 0, len(ids))
	for _, id := range ids {
		result = append(result, layouts[id])
	}
	return result
}
