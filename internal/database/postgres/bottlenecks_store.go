package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BottlenecksStore persists derived evidence. It never runs migrations, captures
// GPS or activates a worker loop implicitly. ProcessNext is its only capture
// processing entry point.
type BottlenecksStore struct {
	database realtimeBeginner
	schema   string
}

func NewBottlenecksStore(pool *pgxpool.Pool) (*BottlenecksStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("bottleneck database is required")
	}
	return &BottlenecksStore{database: pool, schema: "vialis"}, nil
}

func (s *BottlenecksStore) sql(query string) string { return queryInSchema(query, s.schema) }

func rollbackBottlenecks(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = tx.Rollback(ctx)
}

func (s *BottlenecksStore) readRun(ctx context.Context, tx pgx.Tx, name string, lock bool) (bottlenecks.ProcessingRun, error) {
	var r bottlenecks.ProcessingRun
	var config []byte
	query := findBottleneckRunSQL
	if lock {
		query += " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, s.sql(query), name).Scan(&config, &r.ActivatedAt, &r.ActivationCaptureID, &r.Checkpoint, &r.LastReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, bottlenecks.ErrModelNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(config, &r.Model); err != nil {
		return r, err
	}
	if err = r.Model.Validate(); err != nil {
		return r, err
	}
	if r.Model.Name != name || r.Checkpoint < r.ActivationCaptureID {
		return r, fmt.Errorf("invalid persisted measurement run")
	}
	return r, nil
}

// Activate takes the ingestion advisory lock only for the initial watermark.
// This includes in-flight H0011 inserts before selecting max(id), avoiding an
// earlier allocated capture ID becoming visible after activation. Restarting an
// existing model never changes activation or checkpoint, even after raw purge.
func (s *BottlenecksStore) Activate(ctx context.Context, model bottlenecks.PersistenceModel) (bottlenecks.ProcessingRun, error) {
	var empty bottlenecks.ProcessingRun
	if err := model.Validate(); err != nil {
		return empty, err
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer rollbackBottlenecks(tx)

	if _, err = tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock($1)
	`, realtimeLock); err != nil {
		return empty, err
	}

	existing, err := s.readRun(ctx, tx, model.Name, true)
	if err == nil {
		if existing.Model != model {
			return empty, bottlenecks.ErrModelChanged
		}
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		return existing, nil
	}
	if !errors.Is(err, bottlenecks.ErrModelNotFound) {
		return empty, err
	}
	// A table lock also covers manual imports not using H0011's advisory lock.
	if _, err = tx.Exec(ctx, s.sql(`
		LOCK TABLE vialis.capturas_colectivos IN SHARE MODE
	`)); err != nil {
		return empty, err
	}

	config, err := json.Marshal(model)
	if err != nil {
		return empty, err
	}

	_, err = tx.Exec(ctx, s.sql(activateBottleneckModelSQL), model.Name, config)
	if err != nil {
		return empty, err
	}

	run, err := s.readRun(ctx, tx, model.Name, false)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return run, nil
}

// Refuse stale checkpoints rather than treating missing source as empty input.
func (s *BottlenecksStore) checkRetention(ctx context.Context, tx pgx.Tx, reference time.Time, days int) error {
	var expired bool
	err := tx.QueryRow(ctx, checkBottleneckRetentionSQL, reference, days).Scan(&expired)
	if err != nil {
		return err
	}
	if expired {
		return bottlenecks.ErrSourceExpired
	}
	return nil
}
