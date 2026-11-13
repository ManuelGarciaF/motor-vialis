package postgres

import (
	"context"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/precompute"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type precomputeDatabase interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Begin(context.Context) (pgx.Tx, error)
}

// PrecomputeRepository publishes catalog demand and revenue atomically.
type PrecomputeRepository struct{ database precomputeDatabase }

func NewPrecomputeRepository(database precomputeDatabase) *PrecomputeRepository {
	return &PrecomputeRepository{database: database}
}

func (r *PrecomputeRepository) Targets(ctx context.Context, afterID int64, limit int, onlyMissing bool) ([]precompute.Target, error) {
	rows, err := r.database.Query(ctx, `
 SELECT id_recorrido, caudal_pasajeros::bigint, ingreso_economico
 FROM vialis.recorridos
 WHERE id_recorrido > $1
 AND (NOT $2 OR caudal_pasajeros IS NULL OR ingreso_economico IS NULL)
 ORDER BY id_recorrido
 LIMIT NULLIF($3::integer, 0)`, afterID, onlyMissing, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []precompute.Target
	for rows.Next() {
		var target precompute.Target
		if err := rows.Scan(&target.ID, &target.Passengers, &target.Revenue); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (r *PrecomputeRepository) Save(ctx context.Context, target precompute.Target, metrics precompute.Metrics) error {
	tx, err := r.database.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	tag, err := tx.Exec(ctx, `
 UPDATE vialis.recorridos SET
 caudal_pasajeros=$2, ingreso_economico=$3
 WHERE id_recorrido=$1
 AND caudal_pasajeros IS NOT DISTINCT FROM $4::integer
 AND ingreso_economico IS NOT DISTINCT FROM $5::bigint`, target.ID, metrics.Passengers, metrics.Revenue, target.Passengers, target.Revenue)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("line %d disappeared or its catalog metrics changed; not overwritten", target.ID)
	}
	return tx.Commit(ctx)
}

var _ precompute.Repository = (*PrecomputeRepository)(nil)
