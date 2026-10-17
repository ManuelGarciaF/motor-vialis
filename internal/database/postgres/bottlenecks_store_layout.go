package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
)

type bottleneckLayoutSnapshot struct {
	layout       bottlenecks.Layout
	lengthMeters float64
	geometryEWKB []byte
	line, branch string
}

func (s *BottlenecksStore) readLayout(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, id int64) (bottlenecks.PersistedLayout, error) {
	var v bottlenecks.ShapeVersion
	var length float64
	err := tx.QueryRow(ctx, s.sql(findBottleneckLayoutSQL), run.Model.Name, id).Scan(&v.RouteID, &v.Direction, &v.ShapeID, &v.ShapeHash, &length)
	if err != nil {
		return bottlenecks.PersistedLayout{}, err
	}

	layout, err := bottlenecks.NewLayout(v, length, run.Model.NominalMeters)
	return bottlenecks.PersistedLayout{ID: id, Layout: layout}, err
}

// RegisterLayout freezes full geometry, sectors and ordered stops in one
// transaction. Candidate stop/sector associations preserve ambiguity; they are
// not a decision about which branch of a loop actually serves the stop.
func (s *BottlenecksStore) RegisterLayout(ctx context.Context, model string, lineID int64) (bottlenecks.PersistedLayout, error) {
	var empty bottlenecks.PersistedLayout
	if lineID <= 0 {
		return empty, fmt.Errorf("invalid line ID")
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer rollbackBottlenecks(tx)

	run, err := s.readRun(ctx, tx, model, true)
	if err != nil {
		return empty, err
	}

	snapshot, err := s.readCatalogSnapshot(ctx, tx, run, lineID)
	if err != nil {
		return empty, err
	}

	persisted, err := s.persistLayoutSnapshot(ctx, tx, run, lineID, snapshot)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return persisted, nil
}

func (s *BottlenecksStore) readCatalogSnapshot(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, lineID int64) (bottleneckLayoutSnapshot, error) {
	var snapshot bottleneckLayoutSnapshot
	// The catalog lock covers both shape and stops until the public operation
	// commits; helpers must not release it between materialization stages.
	if _, err := tx.Exec(ctx, s.sql(`
		LOCK TABLE vialis.recorridos,
		vialis.recorridos_paradas,
		vialis.paradas IN SHARE MODE
	`)); err != nil {
		return snapshot, err
	}

	var version bottlenecks.ShapeVersion
	err := tx.QueryRow(ctx, s.sql(findBottleneckShapeSnapshotSQL), lineID).Scan(
		&version.RouteID, &version.Direction, &version.ShapeID, &version.ShapeHash,
		&snapshot.lengthMeters, &snapshot.geometryEWKB, &snapshot.line, &snapshot.branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return snapshot, bottlenecks.ErrLineNotFound
	}
	if err != nil {
		return snapshot, err
	}

	snapshot.layout, err = bottlenecks.NewLayout(version, snapshot.lengthMeters, run.Model.NominalMeters)
	return snapshot, err
}

func (s *BottlenecksStore) persistLayoutSnapshot(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, lineID int64, snapshot bottleneckLayoutSnapshot) (bottlenecks.PersistedLayout, error) {
	var empty bottlenecks.PersistedLayout
	version := snapshot.layout.Version()
	var id int64
	err := tx.QueryRow(ctx, s.sql(insertBottleneckLayoutSQL),
		run.Model.Name, version.RouteID, version.Direction, version.ShapeID, version.ShapeHash,
		snapshot.line, snapshot.branch, snapshot.layout.LengthMeters(), snapshot.geometryEWKB).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, s.sql(findBottleneckLayoutIdentitySQL), run.Model.Name, version.RouteID, version.Direction, version.ShapeID, version.ShapeHash).Scan(&id)
		if err != nil {
			return empty, err
		}
		return s.readLayout(ctx, tx, run, id)
	}
	if err != nil {
		return empty, err
	}

	sectors := snapshot.layout.Sectors()
	for _, sector := range sectors {
		// PostGIS fractions use the unrounded source length; domain boundaries
		// are micrometer-stable, with the final endpoint pinned to the full shape.
		start, end := sector.StartMeters/snapshot.lengthMeters, sector.EndMeters/snapshot.lengthMeters
		if sector.Index == len(sectors)-1 {
			end = 1
		}
		_, err = tx.Exec(ctx, s.sql(insertBottleneckSectorSQL), id, sector.Index, sector.StartMeters, sector.EndMeters, start, end)
		if err != nil {
			return empty, err
		}
	}

	var stops []byte
	if err = tx.QueryRow(ctx, s.sql(bottlenecksMaterializeStopsSQL), id, lineID, run.Model.Policy.MaximumDistanceMeters).Scan(&stops); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, s.sql(updateBottleneckLayoutStopsSQL), id, stops); err != nil {
		return empty, err
	}
	return bottlenecks.PersistedLayout{ID: id, Layout: snapshot.layout}, nil
}
