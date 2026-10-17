package bottlenecks

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const MissingCaptureLimit = 4
const MissingUpdates Rejection = "missing_updates"

var (
	ErrCaptureAlreadyProcessed    = errors.New("capture already processed")
	ErrObservationBeforeWatermark = errors.New("observation precedes active measurement watermark")
)

// PendingMeasurement is restart state, not an archive of GPS positions.
// Only the supported entry and accumulated quality counters are necessary.
type PendingMeasurement struct {
	Sector               Sector
	Entered              Crossing
	InteriorObservations int
	MaximumSupportingGap time.Duration
}

// CaptureState belongs to one vehicle and one immutable measurement layout and
// policy. It is serializable; callers must commit it atomically with results.
// Watermark survives closure so older GPS cannot reopen a finalized sequence.
type CaptureState struct {
	VehicleID                   string
	Version                     ShapeVersion
	LengthMeters, NominalMeters float64
	Policy                      MeasurementPolicy
	LastCaptureID               int64
	LastReceivedAt              time.Time
	ClosedThrough               time.Time
	MissingCaptures             int
	Watermark                   *Observation
	Previous                    *Observation
	Pending                     *PendingMeasurement
}

// CaptureInput represents one actual capture, not a processor invocation.
// Failed captures cannot contain observations and never increment absences.
// Observations must already have conservative matching resolved; a processor
// must not finalize matching merely because a technical batch ended.
type CaptureInput struct {
	ID                  int64
	ReceivedAt          time.Time
	Successful, Partial bool
	Observations        []Observation
}

type CaptureMeasurement struct {
	Measurements
	LateObservations int
	Closed           bool
}

// NewCaptureState excludes captures up to activationCaptureID. Activation and
// the global checkpoint must themselves be persisted before processing begins.
func NewCaptureState(layout Layout, policy MeasurementPolicy, vehicleID string, activationCaptureID int64) (CaptureState, error) {
	if len(layout.sectors) == 0 || strings.TrimSpace(vehicleID) == "" || activationCaptureID < 0 {
		return CaptureState{}, fmt.Errorf("invalid capture state identity")
	}
	if err := policy.validate(); err != nil {
		return CaptureState{}, err
	}
	return CaptureState{VehicleID: vehicleID, Version: layout.version, LengthMeters: layout.length, NominalMeters: layout.nominal, Policy: policy, LastCaptureID: activationCaptureID}, nil
}

// MeasureCapture is a pure transition. Technical batch boundaries do not close
// entries. After four eligible captures without a new timestamp, pending entries
// close as incomplete; later GPS can only start an independent sequence.
// Replays return ErrCaptureAlreadyProcessed without emitting results. Consumers
// must distinguish this from operational failures and never persist a partial
// transition. Older observations for an active sequence return
// ErrObservationBeforeWatermark: buffering/rematching belongs to the processor,
// not to this resolved-observation primitive. Only evidence at/before a closed
// sequence's watermark is discarded. Inputs are never mutated.
func MeasureCapture(layout Layout, state CaptureState, input CaptureInput) (CaptureState, CaptureMeasurement, error) {
	empty := CaptureMeasurement{Measurements: Measurements{Passages: []Passage{}, Incomplete: []IncompletePassage{}, Rejections: make(map[Rejection]int)}}
	fail := func(err error) (CaptureState, CaptureMeasurement, error) { return state, CaptureMeasurement{}, err }
	if err := validateCaptureState(layout, state); err != nil {
		return fail(err)
	}
	if input.ID <= 0 {
		return fail(fmt.Errorf("invalid capture metadata"))
	}
	if input.ID <= state.LastCaptureID {
		return fail(ErrCaptureAlreadyProcessed)
	}
	if input.ReceivedAt.IsZero() || (!state.LastReceivedAt.IsZero() && input.ReceivedAt.Before(state.LastReceivedAt)) {
		return fail(fmt.Errorf("invalid capture metadata"))
	}
	if !input.Successful && len(input.Observations) > 0 {
		return fail(fmt.Errorf("failed capture has observations"))
	}
	// Batch validation also rejects conflicting timestamps before any transition.
	if _, err := Measure(layout, input.Observations, state.Policy); err != nil {
		return fail(err)
	}
	rows := append([]Observation(nil), input.Observations...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].ObservedAt.Before(rows[j].ObservedAt) })
	for _, o := range rows {
		if o.VehicleID != state.VehicleID {
			return fail(fmt.Errorf("capture contains another vehicle"))
		}
	}
	next := cloneCaptureState(state)
	result := empty
	cursor := measurementCursor{previous: next.Previous}
	if next.Pending != nil {
		cursor.pending = &passageStart{sector: next.Pending.Sector, crossing: next.Pending.Entered, interior: next.Pending.InteriorObservations, maximumGap: next.Pending.MaximumSupportingGap}
	}
	updated := false
	for _, o := range rows {
		if next.Watermark != nil && !o.ObservedAt.After(next.Watermark.ObservedAt) {
			if o.ObservedAt.Equal(next.Watermark.ObservedAt) {
				if !sameObservation(*next.Watermark, o) {
					return fail(fmt.Errorf("conflicting observation at watermark"))
				}
				result.Duplicates++
			} else if !next.ClosedThrough.IsZero() && !o.ObservedAt.After(next.ClosedThrough) {
				result.LateObservations++
			} else {
				return fail(ErrObservationBeforeWatermark)
			}
			continue
		}
		cursor.advance(layout, o, state.Policy, &result.Measurements)
		copied := o
		next.Watermark = &copied
		updated = true
	}
	if updated {
		next.MissingCaptures = 0
	} else if input.Successful && !input.Partial && next.Watermark != nil {
		// Saturation prevents repeated closures during a prolonged feed absence.
		if next.MissingCaptures < MissingCaptureLimit {
			next.MissingCaptures++
			if next.MissingCaptures == MissingCaptureLimit {
				cursor.abandon(MissingUpdates, &result.Measurements)
				cursor.previous = nil
				next.ClosedThrough = next.Watermark.ObservedAt
				result.Closed = true
			}
		}
	}
	next.Previous = cursor.previous
	next.Pending = nil
	if cursor.pending != nil {
		p := cursor.pending
		next.Pending = &PendingMeasurement{Sector: p.sector, Entered: p.crossing, InteriorObservations: p.interior, MaximumSupportingGap: p.maximumGap}
	}
	next.LastCaptureID = input.ID
	next.LastReceivedAt = input.ReceivedAt
	return next, result, nil
}

func cloneCaptureState(state CaptureState) CaptureState {
	if state.Watermark != nil {
		v := *state.Watermark
		state.Watermark = &v
	}
	if state.Previous != nil {
		v := *state.Previous
		state.Previous = &v
	}
	if state.Pending != nil {
		v := *state.Pending
		state.Pending = &v
	}
	return state
}

func validateCaptureState(layout Layout, state CaptureState) error {
	if len(layout.sectors) == 0 || state.Version != layout.version || state.LengthMeters != layout.length || state.NominalMeters != layout.nominal || strings.TrimSpace(state.VehicleID) == "" || state.LastCaptureID < 0 || state.MissingCaptures < 0 || state.MissingCaptures > MissingCaptureLimit {
		return fmt.Errorf("invalid capture state or layout mismatch")
	}
	if err := state.Policy.validate(); err != nil {
		return err
	}
	if state.Watermark == nil && (state.Previous != nil || state.Pending != nil || state.MissingCaptures != 0) {
		return fmt.Errorf("capture state has no watermark")
	}
	if !state.ClosedThrough.IsZero() && (state.Watermark == nil || state.ClosedThrough.After(state.Watermark.ObservedAt)) {
		return fmt.Errorf("invalid closed watermark")
	}
	for _, o := range []*Observation{state.Watermark, state.Previous} {
		if o == nil {
			continue
		}
		if o.VehicleID != state.VehicleID {
			return fmt.Errorf("capture state vehicle mismatch")
		}
		if _, err := Measure(layout, []Observation{*o}, state.Policy); err != nil {
			return err
		}
	}
	if state.Previous != nil {
		normalized := *state.Watermark
		normalized.ProgressMeters = meters(normalized.ProgressMeters)
		if !state.Previous.ObservedAt.Equal(state.Watermark.ObservedAt) || !sameObservation(*state.Previous, normalized) || rejectObservation(layout, *state.Previous, state.Policy) != "" {
			return fmt.Errorf("invalid previous observation")
		}
	}
	if state.MissingCaptures == MissingCaptureLimit && (state.Previous != nil || state.Pending != nil) {
		return fmt.Errorf("closed capture state has continuity")
	}
	if p := state.Pending; p != nil {
		if state.Previous == nil || p.Sector.Index < 0 || p.Sector.Index >= len(layout.sectors) || p.Sector != layout.sectors[p.Sector.Index] || p.InteriorObservations < 0 || p.MaximumSupportingGap < 0 || p.MaximumSupportingGap > state.Policy.MaximumGap || p.Entered.At.IsZero() || p.Entered.BeforeAt.IsZero() || p.Entered.AfterAt.IsZero() || p.Entered.At.Before(p.Entered.BeforeAt) || p.Entered.At.After(p.Entered.AfterAt) || p.Entered.AfterAt.After(state.Previous.ObservedAt) || p.Entered.AfterAt.Sub(p.Entered.BeforeAt) > state.Policy.MaximumGap || state.Previous.ProgressMeters < p.Sector.StartMeters || state.Previous.ProgressMeters >= p.Sector.EndMeters {
			return fmt.Errorf("invalid pending measurement")
		}
	}
	return nil
}
