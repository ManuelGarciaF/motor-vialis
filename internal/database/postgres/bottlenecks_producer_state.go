package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
)

// Legacy resolved transitions can run against the original six-table schema.
// Once producer mode is selected it remains selected even for zero vehicles.
func (s *BottlenecksStore) rejectProducerMode(ctx context.Context, tx pgx.Tx, model string) error {
	var installed bool
	if err := tx.QueryRow(ctx, `
		SELECT to_regclass(format('%I.h0012_productores', $1::text)) IS NOT NULL
	`, s.schema).Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, s.sql(`
		SELECT EXISTS(SELECT 1 FROM vialis.h0012_productores WHERE modelo=$1)
	`), model).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return bottlenecks.ErrProducerModeConflict
	}
	return nil
}

func (s *BottlenecksStore) selectProducerMode(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun) error {
	var producer, legacy bool
	err := tx.QueryRow(ctx, s.sql(findBottleneckProcessingModesSQL), run.Model.Name).Scan(&producer, &legacy)
	if err != nil {
		return err
	}
	if legacy || !producer && run.Checkpoint != run.ActivationCaptureID {
		return bottlenecks.ErrProducerModeConflict
	}
	if !producer {
		_, err = tx.Exec(ctx, s.sql(`
			INSERT INTO vialis.h0012_productores(modelo) VALUES($1)
		`), run.Model.Name)
	}
	return err
}

func (s *BottlenecksStore) readProducerStates(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun) ([]bottlenecks.ProducerState, error) {
	rows, err := tx.Query(ctx, s.sql(listProducerStatesSQL), run.Model.Name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := []bottlenecks.ProducerState{}
	for rows.Next() {
		var vehicle string
		var checkpoint int64
		var data []byte
		var state bottlenecks.ProducerState
		if err = rows.Scan(&vehicle, &checkpoint, &data); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(data, &state); err != nil {
			return nil, err
		}
		if err = state.Validate(); err != nil {
			return nil, err
		}
		if state.VehicleID != vehicle || state.LastCaptureID != checkpoint || checkpoint != run.Checkpoint || state.Model != run.Model || state.ActivationCaptureID != run.ActivationCaptureID {
			return nil, fmt.Errorf("inconsistent producer recovery state")
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

// ProducerSchemaReady checks explicit migration prerequisites without creating
// tables, registering a processing mode or activating a model.
func (s *BottlenecksStore) ProducerSchemaReady(ctx context.Context) (bool, error) {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollbackBottlenecks(tx)
	var ready bool
	if err = tx.QueryRow(ctx, checkProducerSchemaSQL, s.schema).Scan(&ready); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return ready, nil
}

// LoadProducerStates recovers a coherent committed source checkpoint and receipt
// states. It does not register a processing mode, activate, or mutate any model.
func (s *BottlenecksStore) LoadProducerStates(ctx context.Context, model string) (bottlenecks.ProcessingRun, []bottlenecks.ProducerState, error) {
	empty := bottlenecks.ProcessingRun{}
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return empty, nil, err
	}
	defer rollbackBottlenecks(tx)
	run, err := s.readRun(ctx, tx, model, true)
	if err != nil {
		return empty, nil, err
	}
	var producer, legacy bool
	if err = tx.QueryRow(ctx, s.sql(findBottleneckProcessingModesSQL), model).Scan(&producer, &legacy); err != nil {
		return empty, nil, err
	}
	if legacy || !producer && run.Checkpoint != run.ActivationCaptureID {
		return empty, nil, bottlenecks.ErrProducerModeConflict
	}
	states, err := s.readProducerStates(ctx, tx, run)
	if err != nil {
		return empty, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, nil, err
	}
	return run, states, nil
}

func (s *BottlenecksStore) persistProducerState(ctx context.Context, tx pgx.Tx, state bottlenecks.ProducerState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, s.sql(upsertProducerStateSQL), state.Model.Name, state.VehicleID, state.LastCaptureID, data)
	return err
}
