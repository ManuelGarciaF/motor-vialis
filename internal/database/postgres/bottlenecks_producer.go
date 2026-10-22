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

type producerSourceCapture struct {
	id       int64
	received time.Time
	partial  bool
	inserted int64
}

// ProcessNext owns one independent Bottlenecks transaction. It reads one actual H0011
// capture, advances receipt state, and finalizes only closed evidence. Neither
// idle cycles nor SQL retries count as captures. No ingestion advisory lock is
// acquired; checkpoint/state/results become visible only after this commit.
func (s *BottlenecksStore) ProcessNext(ctx context.Context, model string) (bottlenecks.ProducerProgress, error) {
	empty := bottlenecks.ProducerProgress{}
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer rollbackBottlenecks(tx)
	run, err := s.readRun(ctx, tx, model, true)
	if err != nil {
		return empty, err
	}
	reference := run.ActivatedAt
	if run.LastReceivedAt != nil {
		reference = *run.LastReceivedAt
	}
	if err = s.checkRetention(ctx, tx, reference, run.Model.RawRetentionDays); err != nil {
		return empty, err
	}
	// Recursive ACCESS SHARE locks pin the raw parent and its partitions until
	// commit without conflicting with inserts. H0011 drops expired partitions
	// before deleting capture metadata, so pinning also prevents that purge from
	// removing source captures while this transaction selects its next one.
	if _, err = tx.Exec(ctx, s.sql(`
		LOCK TABLE vialis.posiciones_colectivos_raw IN ACCESS SHARE MODE
	`)); err != nil {
		return empty, err
	}
	// The UTC date may change while waiting for an in-flight purge. Validate the
	// original checkpoint reference again, not a newer surviving capture that
	// could conceal source loss. This also protects the idle return path.
	if err = s.checkRetention(ctx, tx, reference, run.Model.RawRetentionDays); err != nil {
		return empty, err
	}
	states, err := s.readProducerStates(ctx, tx, run)
	if err != nil {
		return empty, err
	}
	if err = s.checkProducerOpenRetention(ctx, tx, run, states); err != nil {
		return empty, err
	}
	layouts := make(map[int64]bottlenecks.PersistedLayout)
	for _, state := range states {
		if state.Open == nil {
			continue
		}
		for _, ref := range state.Open.Layouts {
			layout, exists := layouts[ref.ID]
			if !exists {
				layout, err = s.readLayout(ctx, tx, run, ref.ID)
				if errors.Is(err, pgx.ErrNoRows) {
					return empty, bottlenecks.ErrProducerGeometryUnavailable
				}
				if err != nil {
					return empty, err
				}
			}
			if layout.Layout.Version() != ref.Version || layout.Layout.LengthMeters() != ref.LengthMeters || layout.Layout.NominalMeters() != ref.NominalMeters {
				return empty, bottlenecks.ErrProducerGeometryUnavailable
			}
			layouts[ref.ID] = layout
		}
	}
	var capture producerSourceCapture
	err = tx.QueryRow(ctx, s.sql(findNextProducerCaptureSQL), run.Checkpoint).Scan(&capture.id, &capture.received, &capture.partial, &capture.inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		return bottlenecks.ProducerProgress{Checkpoint: run.Checkpoint}, nil
	}
	if err != nil {
		return empty, err
	}
	if capture.inserted < 0 || run.LastReceivedAt != nil && capture.received.Before(*run.LastReceivedAt) {
		return empty, fmt.Errorf("invalid producer capture metadata")
	}
	if err = s.checkRetention(ctx, tx, capture.received, run.Model.RawRetentionDays); err != nil {
		return empty, err
	}
	var actual int64
	if err = tx.QueryRow(ctx, s.sql(`
		SELECT count(*)
		FROM vialis.posiciones_colectivos_raw
		WHERE id_captura = $1
	`), capture.id).Scan(&actual); err != nil {
		return empty, err
	}
	if actual != capture.inserted {
		return empty, bottlenecks.ErrProducerSourceIncomplete
	}
	bounds := producerSourceBounds{first: capture.id, last: capture.id}
	if err = s.ensureProducerLayouts(ctx, tx, run, bounds, layouts); err != nil {
		return empty, err
	}
	if err = s.prepareProducerProjection(ctx, tx, run, layouts); err != nil {
		return empty, err
	}
	rows, err := s.readProducerCandidates(ctx, tx, run, bounds)
	if err != nil {
		return empty, err
	}
	if int64(len(rows)) != capture.inserted {
		return empty, bottlenecks.ErrProducerSourceIncomplete
	}
	result, err := s.processProducerVehicles(ctx, tx, run, capture, states, rows, layouts)
	if err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, s.sql(updateBottleneckCheckpointSQL), model, capture.id, capture.received); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *BottlenecksStore) processProducerVehicles(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, capture producerSourceCapture, states []bottlenecks.ProducerState, rows []bottlenecks.ProducerObservation, layouts map[int64]bottlenecks.PersistedLayout) (bottlenecks.ProducerProgress, error) {
	empty := bottlenecks.ProducerProgress{}
	byVehicle := make(map[string][]bottlenecks.ProducerObservation)
	known := make(map[string]bottlenecks.ProducerState, len(states))
	for _, state := range states {
		known[state.VehicleID] = state
	}
	for _, row := range rows {
		vehicle := row.Candidate.VehicleID
		byVehicle[vehicle] = append(byVehicle[vehicle], row)
		if _, exists := known[vehicle]; !exists {
			state, err := bottlenecks.NewProducerState(run.Model, vehicle, run.ActivationCaptureID)
			if err != nil {
				return empty, err
			}
			known[vehicle] = state
		}
	}
	vehicles := make([]string, 0, len(known))
	for vehicle := range known {
		vehicles = append(vehicles, vehicle)
	}
	sort.Strings(vehicles)
	result := bottlenecks.ProducerProgress{Processed: true, Checkpoint: capture.id, Vehicles: len(vehicles), Rejections: make(map[bottlenecks.Rejection]int)}
	nextStates := make([]bottlenecks.ProducerState, 0, len(vehicles))
	for _, vehicle := range vehicles {
		state := known[vehicle]
		input := bottlenecks.ProducerCapture{ID: capture.id, ReceivedAt: capture.received, Successful: true, Partial: capture.partial, New: byVehicle[vehicle]}
		newer := false
		for _, row := range input.New {
			if row.Candidate.ObservedAt.After(state.LatestSeenAt) {
				newer = true
				break
			}
		}
		if state.Open != nil && !capture.partial && !newer && state.MissingCaptures == bottlenecks.MissingCaptureLimit-1 {
			bounds := producerSourceBounds{first: state.Open.FirstCaptureID, last: capture.id, vehicle: &vehicle}
			if !state.ClosedThrough.IsZero() {
				closed := state.ClosedThrough
				bounds.closedThrough = &closed
			}
			// Every accepted replay scope already has an OPEN layout reference;
			// current-capture scopes were registered before preparing the index.
			// Canonical membership/fingerprint validation rejects altered raw.
			replay, err := s.readProducerCandidates(ctx, tx, run, bounds)
			if err != nil {
				return empty, err
			}
			input.Replay = replay
		}
		vehicleLayouts, err := producerVehicleLayouts(state, input, layouts)
		if err != nil {
			return empty, err
		}
		next, produced, err := bottlenecks.ProduceCapture(state, input, vehicleLayouts)
		if err != nil {
			return empty, fmt.Errorf("%w: %v", bottlenecks.ErrProducerSourceIncomplete, err)
		}
		if err = s.persistProducerResult(ctx, tx, run, capture.id, next, produced, layouts, &result); err != nil {
			return empty, err
		}
		nextStates = append(nextStates, next)
	}
	if err := s.checkProducerOpenRetention(ctx, tx, run, nextStates); err != nil {
		return empty, err
	}
	if err := s.persistProducerStates(ctx, tx, run, capture.id, nextStates); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *BottlenecksStore) persistProducerResult(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, captureID int64, state bottlenecks.ProducerState, produced bottlenecks.ProducerResult, layouts map[int64]bottlenecks.PersistedLayout, progress *bottlenecks.ProducerProgress) error {
	progress.LateObservations += produced.LateObservations
	progress.Duplicates += produced.Duplicates
	if produced.Closed {
		progress.ClosedVehicles++
	}
	for _, segment := range produced.Segments {
		layout, exists := layouts[segment.LayoutID]
		if !exists || layout.Layout.Version() != segment.Scope.Version {
			return bottlenecks.ErrProducerGeometryUnavailable
		}
		// This is support validation of finalized output, not a receipt transition.
		// Absence accounting belongs exclusively to ProducerState/ProduceCapture.
		if err := validateMeasurementSupport(layout.Layout, state.VehicleID, run.Model.Policy, state.ClosedThrough, segment.Measurements); err != nil {
			return err
		}
		for _, p := range segment.Measurements.Passages {
			record := bottleneckMeasurementRecord{layoutID: segment.LayoutID, captureID: captureID, vehicle: p.VehicleID, scope: p.Scope, sector: p.Sector, entry: p.Entered, complete: true, duration: p.Duration, speedKPH: p.SpeedKPH, payload: p}
			if err := s.insertMeasurement(ctx, tx, run, record); err != nil {
				return err
			}
			progress.Passages++
		}
		for _, p := range segment.Measurements.Incomplete {
			record := bottleneckMeasurementRecord{layoutID: segment.LayoutID, captureID: captureID, vehicle: p.VehicleID, scope: p.Scope, sector: p.Sector, entry: p.Entered, reason: string(p.Reason), payload: p}
			if err := s.insertMeasurement(ctx, tx, run, record); err != nil {
				return err
			}
			progress.Incomplete++
		}
		progress.SpatiallyUnique += segment.SpatiallyUnique
		progress.TemporallyResolved += segment.TemporallyResolved
		progress.Ambiguous += segment.Ambiguous
		progress.Unmatched += segment.Unmatched
		for reason, count := range segment.Measurements.Rejections {
			progress.Rejections[reason] += count
		}
	}
	return nil
}

var _ bottlenecks.ProducerRepository = (*BottlenecksStore)(nil)
