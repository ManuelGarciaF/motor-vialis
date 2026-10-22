package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
)

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

// ProducerSchemaReady checks the operational schema without creating tables or
// activating a measurement configuration.
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
// states. It does not activate or mutate the measurement configuration.
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
	states, err := s.readProducerStates(ctx, tx, run)
	if err != nil {
		return empty, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, nil, err
	}
	return run, states, nil
}

func (s *BottlenecksStore) persistProducerStates(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, captureID int64, states []bottlenecks.ProducerState) error {
	if len(states) == 0 {
		return nil
	}
	data, err := json.Marshal(states)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, s.sql(upsertProducerStateSQL), run.Model.Name, captureID, data)
	return err
}

// Expiry is monotone in the oldest OPEN timestamp: checking the minimum once
// protects every unit, including across UTC midnight, without N database calls.
func (s *BottlenecksStore) checkProducerOpenRetention(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, states []bottlenecks.ProducerState) error {
	var oldest time.Time
	for _, state := range states {
		if state.Open != nil && (oldest.IsZero() || state.Open.EarliestAt.Before(oldest)) {
			oldest = state.Open.EarliestAt
		}
	}
	if oldest.IsZero() {
		return nil
	}
	return s.checkRetention(ctx, tx, oldest, run.Model.RawRetentionDays)
}
