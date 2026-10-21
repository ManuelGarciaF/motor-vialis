package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
)

type bottleneckStreamKey struct {
	layout  int64
	vehicle string
}

// bottleneckCaptureCommit exists only while its public operation owns the
// model's row lock. Helpers mutate this transaction-local bookkeeping only.
type bottleneckCaptureCommit struct {
	run      bottlenecks.ProcessingRun
	received time.Time
	partial  bool
	existing map[bottleneckStreamKey]bottlenecks.CaptureState
	seen     map[bottleneckStreamKey]bool
	layouts  map[int64]bottlenecks.PersistedLayout
	result   bottlenecks.CaptureCommit
}

// CommitCapture accepts domain transitions, not caller-supplied API records.
// All streams, results, diagnostics and checkpoint commit together. The row
// lock serializes processors of the same model without holding H0011's lock.
func (s *BottlenecksStore) CommitCapture(ctx context.Context, model string, expected, captureID int64, transitions []bottlenecks.StreamTransition) (bottlenecks.CaptureCommit, error) {
	empty := bottlenecks.CaptureCommit{}
	if expected < 0 || captureID <= 0 {
		return empty, fmt.Errorf("invalid capture commit")
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer rollbackBottlenecks(tx)

	capture, err := s.prepareCaptureCommit(ctx, tx, model, expected, captureID, len(transitions))
	if err != nil {
		return empty, err
	}

	for _, transition := range transitions {
		if err = s.persistStreamTransition(ctx, tx, capture, transition); err != nil {
			return empty, err
		}
	}
	if err = s.finishCaptureCommit(ctx, tx, capture); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return capture.result, nil
}

func (s *BottlenecksStore) prepareCaptureCommit(ctx context.Context, tx pgx.Tx, model string, expected, captureID int64, transitionCount int) (*bottleneckCaptureCommit, error) {
	run, err := s.readRun(ctx, tx, model, true)
	if err != nil {
		return nil, err
	}
	if err = s.rejectProducerMode(ctx, tx, model); err != nil {
		return nil, err
	}
	if captureID <= run.Checkpoint {
		return nil, bottlenecks.ErrCaptureAlreadyProcessed
	}
	if expected != run.Checkpoint {
		return nil, bottlenecks.ErrCheckpointConflict
	}

	reference := run.ActivatedAt
	if run.LastReceivedAt != nil {
		reference = *run.LastReceivedAt
	}
	if err = s.checkRetention(ctx, tx, reference, run.Model.RawRetentionDays); err != nil {
		return nil, err
	}

	capture := &bottleneckCaptureCommit{run: run}
	var nextID int64
	err = tx.QueryRow(ctx, s.sql(findNextBottleneckCaptureSQL), expected).Scan(&nextID, &capture.received, &capture.partial)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && nextID != captureID {
		return nil, bottlenecks.ErrCaptureOutOfOrder
	}
	if err != nil {
		return nil, err
	}
	if run.LastReceivedAt != nil && capture.received.Before(*run.LastReceivedAt) {
		return nil, fmt.Errorf("capture receipt regressed")
	}
	if err = s.checkRetention(ctx, tx, capture.received, run.Model.RawRetentionDays); err != nil {
		return nil, err
	}

	streams, err := s.loadStates(ctx, tx, run)
	if err != nil {
		return nil, err
	}
	capture.existing = make(map[bottleneckStreamKey]bottlenecks.CaptureState, len(streams))
	for _, stream := range streams {
		capture.existing[bottleneckStreamKey{stream.LayoutID, stream.State.VehicleID}] = stream.State
	}
	capture.seen = make(map[bottleneckStreamKey]bool, transitionCount)
	capture.layouts = make(map[int64]bottlenecks.PersistedLayout)
	capture.result = bottlenecks.CaptureCommit{Checkpoint: captureID, Rejections: make(map[bottlenecks.Rejection]int)}
	return capture, nil
}

func (s *BottlenecksStore) persistStreamTransition(ctx context.Context, tx pgx.Tx, capture *bottleneckCaptureCommit, transition bottlenecks.StreamTransition) error {
	state := transition.State
	key := bottleneckStreamKey{transition.LayoutID, state.VehicleID}
	if capture.seen[key] {
		return fmt.Errorf("duplicate stream transition")
	}
	capture.seen[key] = true
	old, exists := capture.existing[key]
	if exists && transition.PreviousCaptureID != old.LastCaptureID || !exists && transition.PreviousCaptureID != 0 {
		return bottlenecks.ErrCheckpointConflict
	}
	if state.LastCaptureID != capture.result.Checkpoint || !state.LastReceivedAt.Equal(capture.received) || state.Policy != capture.run.Model.Policy {
		return fmt.Errorf("transition capture/model mismatch")
	}

	layout, ok := capture.layouts[transition.LayoutID]
	if !ok {
		var err error
		layout, err = s.readLayout(ctx, tx, capture.run, transition.LayoutID)
		if err != nil {
			return err
		}
		capture.layouts[transition.LayoutID] = layout
	}
	if err := state.Validate(layout.Layout); err != nil {
		return err
	}
	if err := validateStoredMeasurement(layout.Layout, state, transition.Measurement); err != nil {
		return err
	}
	if exists && old.Watermark != nil && (state.Watermark == nil || state.Watermark.ObservedAt.Before(old.Watermark.ObservedAt)) {
		return fmt.Errorf("GPS watermark regressed")
	}
	if exists && state.ClosedThrough.Before(old.ClosedThrough) {
		return fmt.Errorf("closed watermark regressed")
	}
	if err := validateStoredAbsence(old, exists, state, transition.Measurement, capture.partial); err != nil {
		return err
	}

	for _, p := range transition.Measurement.Passages {
		record := bottleneckMeasurementRecord{
			layoutID: transition.LayoutID, captureID: capture.result.Checkpoint,
			vehicle: p.VehicleID, scope: p.Scope, sector: p.Sector, entry: p.Entered,
			complete: true, duration: p.Duration, speedKPH: p.SpeedKPH, payload: p,
		}
		if err := s.insertMeasurement(ctx, tx, capture.run, record); err != nil {
			return err
		}
		capture.result.Passages++
	}

	for _, p := range transition.Measurement.Incomplete {
		record := bottleneckMeasurementRecord{
			layoutID: transition.LayoutID, captureID: capture.result.Checkpoint,
			vehicle: p.VehicleID, scope: p.Scope, sector: p.Sector, entry: p.Entered,
			reason: string(p.Reason), payload: p,
		}
		if err := s.insertMeasurement(ctx, tx, capture.run, record); err != nil {
			return err
		}
		capture.result.Incomplete++
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, s.sql(upsertBottleneckStateSQL), capture.run.Model.Name, transition.LayoutID, state.VehicleID, capture.result.Checkpoint, data)
	if err != nil {
		return err
	}

	m := transition.Measurement
	capture.result.Duplicates += m.Duplicates
	capture.result.LateObservations += m.LateObservations
	if m.Closed {
		capture.result.ClosedStreams++
	}
	for reason, count := range m.Rejections {
		capture.result.Rejections[reason] += count
	}
	return nil
}

func (s *BottlenecksStore) finishCaptureCommit(ctx context.Context, tx pgx.Tx, capture *bottleneckCaptureCommit) error {
	// This must follow per-stream writes: an omitted stream aborts the same
	// transaction rather than publishing a partial capture as a new checkpoint.
	for key := range capture.existing {
		if !capture.seen[key] {
			return fmt.Errorf("capture commit omitted a tracked stream")
		}
	}

	diagnostic, err := json.Marshal(capture.result)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, s.sql(insertBottleneckDiagnosticSQL), capture.run.Model.Name, capture.result.Checkpoint, diagnostic); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, s.sql(updateBottleneckCheckpointSQL), capture.run.Model.Name, capture.result.Checkpoint, capture.received)
	return err
}
