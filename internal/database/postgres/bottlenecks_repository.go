package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed find_bottleneck_candidates.sql
var findBottleneckCandidatesSQL string

//go:embed list_bottleneck_analysis_targets.sql
var listBottleneckAnalysisTargetsSQL string

// BottlenecksRepository is an experimental, read-only trajectory source.
// ST_ClosestPoint(geography) requires PostGIS >= 3.4. It never repairs shapes.
type BottlenecksRepository struct {
	database *pgxpool.Pool
	schema   string
}

func NewBottlenecksRepository(pool *pgxpool.Pool) *BottlenecksRepository {
	return &BottlenecksRepository{database: pool, schema: "vialis"}
}

func (r *BottlenecksRepository) Load(ctx context.Context, id int64, window bottlenecks.Window, radius float64) (bottlenecks.Dataset, error) {
	if err := window.Validate(); err != nil {
		return bottlenecks.Dataset{}, err
	}
	if id <= 0 || math.IsNaN(radius) || math.IsInf(radius, 0) || radius < 0 {
		return bottlenecks.Dataset{}, fmt.Errorf("invalid trajectory query")
	}
	tx, err := r.database.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return bottlenecks.Dataset{}, fmt.Errorf("begin trajectory snapshot: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	data, err := r.load(ctx, tx, id, window, radius)
	if err != nil {
		return bottlenecks.Dataset{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return bottlenecks.Dataset{}, fmt.Errorf("commit trajectory snapshot: %w", err)
	}
	return data, nil
}

func (r *BottlenecksRepository) load(ctx context.Context, tx pgx.Tx, id int64, window bottlenecks.Window, radius float64) (bottlenecks.Dataset, error) {
	data := bottlenecks.Dataset{LineID: id, Observations: []bottlenecks.CandidateObservation{}}
	err := tx.QueryRow(ctx, queryInSchema(`SELECT gtfs_route_id,direction_id,gtfs_shape_id,
 md5(encode(ST_AsEWKB(geom),'hex')),ST_Length(geom::geography),linea,ramal
 FROM vialis.recorridos WHERE id_recorrido=$1`, r.schema), id).Scan(
		&data.Version.RouteID, &data.Version.Direction, &data.Version.ShapeID, &data.Version.ShapeHash, &data.LengthMeters, &data.Line, &data.Branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return bottlenecks.Dataset{}, bottlenecks.ErrLineNotFound
	}
	if err != nil {
		return bottlenecks.Dataset{}, fmt.Errorf("read trajectory shape: %w", err)
	}
	rows, err := tx.Query(ctx, queryInSchema(findBottleneckCandidatesSQL, r.schema), id, window.From, window.To, radius)
	if err != nil {
		return bottlenecks.Dataset{}, fmt.Errorf("query trajectory projections: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var o bottlenecks.CandidateObservation
		var progress, distances []float64
		if err := rows.Scan(&o.VehicleID, &o.Scope.ProviderRouteID, &o.Scope.ProviderDirection,
			&o.Scope.Version.RouteID, &o.Scope.Version.Direction, &o.Scope.Version.ShapeID, &o.Scope.Version.ShapeHash,
			&o.ObservedAt, &progress, &distances); err != nil {
			return bottlenecks.Dataset{}, fmt.Errorf("scan trajectory projections: %w", err)
		}
		if len(progress) != len(distances) {
			return bottlenecks.Dataset{}, fmt.Errorf("projection arrays have different lengths")
		}
		for i, p := range progress {
			o.Candidates = append(o.Candidates, bottlenecks.Projection{ProgressMeters: p, DistanceMeters: distances[i]})
		}
		data.Observations = append(data.Observations, o)
	}
	if err := rows.Err(); err != nil {
		return bottlenecks.Dataset{}, fmt.Errorf("iterate trajectory projections: %w", err)
	}
	return data, nil
}

func (r *BottlenecksRepository) ListTargets(ctx context.Context, window bottlenecks.Window) ([]bottlenecks.AnalysisTarget, error) {
	return r.listTargets(ctx, func(ctx context.Context, sql string, args ...any) (rowIterator, error) {
		return r.database.Query(ctx, sql, args...)
	}, window)
}

func (r *BottlenecksRepository) listTargets(ctx context.Context, query queryFunc, window bottlenecks.Window) ([]bottlenecks.AnalysisTarget, error) {
	if err := window.Validate(); err != nil {
		return nil, err
	}
	rows, err := query(ctx, queryInSchema(listBottleneckAnalysisTargetsSQL, r.schema), window.From, window.To)
	if err != nil {
		return nil, fmt.Errorf("list analysis targets: %w", err)
	}
	defer rows.Close()
	targets := []bottlenecks.AnalysisTarget{}
	for rows.Next() {
		var t bottlenecks.AnalysisTarget
		if err := rows.Scan(&t.LineID, &t.Version.RouteID, &t.Version.Direction, &t.Version.ShapeID, &t.Version.ShapeHash, &t.ExpectedRawPositions); err != nil {
			return nil, fmt.Errorf("scan analysis target: %w", err)
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate analysis targets: %w", err)
	}
	return targets, nil
}

var _ bottlenecks.TargetRepository = (*BottlenecksRepository)(nil)
