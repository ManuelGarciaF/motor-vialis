package bottlenecks

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const MissingCaptureLimit = 4
const MissingUpdates Rejection = "missing_updates"

var ErrCaptureAlreadyProcessed = errors.New("capture already processed")

// ProducerLayout identifies a retained static layout without serializing GPS or
// projection candidates. A layout ID cannot change geometry or policy on restart.
type ProducerLayout struct {
	ID                          int64
	Version                     ShapeVersion
	LengthMeters, NominalMeters float64
}

// OpenProducerSequence describes the canonical raw needed for final replay.
// Receipt bounds select rows by original insertion capture, not GPS time alone.
// The count/fingerprint detect incomplete or changed replay; the source remains
// responsible for retaining all raw and supplying its original alternatives.
type OpenProducerSequence struct {
	FirstCaptureID, LastCaptureID int64
	EarliestAt, LatestAt          time.Time
	Observations                  int
	EvidenceFingerprint           string
	Layouts                       []ProducerLayout
	Scopes                        []Scope
}

// ProducerState tracks reception separately from finalized measurement. It holds
// no GPS coordinates, observations, or candidate arrays. ClosedThrough survives
// restarts and later sequences so finalized evidence can never reopen.
type ProducerState struct {
	Model                       PersistenceModel
	VehicleID                   string
	ActivationCaptureID         int64
	LastCaptureID               int64
	LastReceivedAt              time.Time
	LatestSeenAt, ClosedThrough time.Time
	MissingCaptures             int
	Open                        *OpenProducerSequence
}

// ProducerObservation is a source-provided canonical raw row projected against
// its exact retained layout. CaptureID is its first-receipt capture, not a GPS
// timestamp ordinal. Candidate lists must be the original alternatives.
type ProducerObservation struct {
	CaptureID int64
	LayoutID  int64
	Candidate CandidateObservation
}

// ProducerCapture supplies newly inserted canonical rows for this vehicle.
// Replay is required only on the fourth eligible absence: every accepted OPEN
// row, including late insertions, from FirstCaptureID through this capture and
// strictly after ClosedThrough. Rows of other scopes must not be filtered out.
type ProducerCapture struct {
	ID                  int64
	ReceivedAt          time.Time
	Successful, Partial bool
	New                 []ProducerObservation
	Replay              []ProducerObservation
}

// ProducedSegment is a final event-time-contiguous scope segment. Repeated
// A->B->A scopes produce separate segments even if their layout IDs are equal.
type ProducedSegment struct {
	LayoutID                                                  int64
	Scope                                                     Scope
	Measurements                                              Measurements
	SpatiallyUnique, TemporallyResolved, Ambiguous, Unmatched int
}

type ProducerResult struct {
	Closed                       bool
	LateObservations, Duplicates int
	Segments                     []ProducedSegment
}

func NewProducerState(model PersistenceModel, vehicle string, activationCaptureID int64) (ProducerState, error) {
	state := ProducerState{Model: model, VehicleID: vehicle, ActivationCaptureID: activationCaptureID, LastCaptureID: activationCaptureID}
	if err := state.Validate(); err != nil {
		return ProducerState{}, err
	}
	return state, nil
}

// ProduceCapture never publishes an OPEN measurement prefix. A later anchor or
// older OPEN insertion may change matching and interpolation behind that prefix.
// All accepted rows are rematched at final closure; scope barriers are evaluated
// in GPS order at that time, not irreversibly in receipt order. Limits/policies
// are explicit in Model; resource exhaustion or missing replay is an error and
// must not be converted to a synthetic closure. Callers atomically persist the
// returned state/results with their global source checkpoint.
func ProduceCapture(state ProducerState, capture ProducerCapture, suppliedLayouts []PersistedLayout) (ProducerState, ProducerResult, error) {
	fail := func(err error) (ProducerState, ProducerResult, error) { return state, ProducerResult{}, err }
	if err := state.Validate(); err != nil {
		return fail(err)
	}
	if capture.ID <= 0 {
		return fail(fmt.Errorf("invalid producer capture ID"))
	}
	if capture.ID <= state.LastCaptureID {
		return fail(ErrCaptureAlreadyProcessed)
	}
	if capture.ReceivedAt.IsZero() || (!state.LastReceivedAt.IsZero() && capture.ReceivedAt.Before(state.LastReceivedAt)) {
		return fail(fmt.Errorf("invalid producer capture receipt"))
	}
	if !capture.Successful && (len(capture.New) != 0 || len(capture.Replay) != 0) {
		return fail(fmt.Errorf("failed producer capture has evidence"))
	}

	layouts, err := producerLayouts(state.Model, suppliedLayouts)
	if err != nil {
		return fail(err)
	}
	rows, duplicates, err := canonicalProducerRows(capture.New, state.VehicleID, layouts)
	if err != nil {
		return fail(err)
	}
	next := cloneProducerState(state)
	result := ProducerResult{Segments: []ProducedSegment{}, Duplicates: duplicates}
	updated := false
	for _, row := range rows {
		if row.CaptureID != capture.ID {
			return fail(fmt.Errorf("new producer row belongs to another capture"))
		}
		at := row.Candidate.ObservedAt
		if !next.ClosedThrough.IsZero() && !at.After(next.ClosedThrough) {
			result.LateObservations++
			continue
		}
		if next.Open == nil {
			next.Open = &OpenProducerSequence{FirstCaptureID: row.CaptureID, LastCaptureID: row.CaptureID,
				EarliestAt: at, LatestAt: at, EvidenceFingerprint: strings.Repeat("0", 64), Layouts: []ProducerLayout{}}
		}
		if err := accumulateProducerEvidence(next.Open, row, layouts[row.LayoutID]); err != nil {
			return fail(err)
		}
		if next.LatestSeenAt.IsZero() || at.After(next.LatestSeenAt) {
			next.LatestSeenAt = at
			updated = true
		}
	}

	if updated {
		next.MissingCaptures = 0
	} else if capture.Successful && !capture.Partial && next.Open != nil {
		next.MissingCaptures++
	}
	if next.Open != nil && next.MissingCaptures == MissingCaptureLimit {
		segments, err := finalizeProducerSequence(next, capture, layouts)
		if err != nil {
			return fail(err)
		}
		result.Closed, result.Segments = true, segments
		next.ClosedThrough = next.LatestSeenAt
		next.Open = nil
	} else if len(capture.Replay) != 0 {
		return fail(fmt.Errorf("producer replay supplied before final closure"))
	}
	next.LastCaptureID, next.LastReceivedAt = capture.ID, capture.ReceivedAt
	if err := next.Validate(); err != nil {
		return fail(err)
	}
	return next, result, nil
}

func cloneProducerState(state ProducerState) ProducerState {
	if state.Open != nil {
		open := *state.Open
		open.Layouts = append([]ProducerLayout(nil), open.Layouts...)
		open.Scopes = append([]Scope(nil), open.Scopes...)
		state.Open = &open
	}
	return state
}

func producerLayouts(model PersistenceModel, supplied []PersistedLayout) (map[int64]Layout, error) {
	layouts := make(map[int64]Layout, len(supplied))
	for _, supplied := range supplied {
		layout := supplied.Layout
		if supplied.ID <= 0 || len(layout.sectors) == 0 || layout.nominal != meters(model.NominalMeters) {
			return nil, fmt.Errorf("invalid producer layout")
		}
		if _, exists := layouts[supplied.ID]; exists {
			return nil, fmt.Errorf("duplicate producer layout ID")
		}
		layouts[supplied.ID] = layout
	}
	return layouts, nil
}

func producerLayoutReference(id int64, layout Layout) ProducerLayout {
	return ProducerLayout{ID: id, Version: layout.version, LengthMeters: layout.length, NominalMeters: layout.nominal}
}

func (state ProducerState) Validate() error {
	if err := state.Model.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(state.VehicleID) == "" || state.ActivationCaptureID < 0 || state.LastCaptureID < state.ActivationCaptureID ||
		state.MissingCaptures < 0 || state.MissingCaptures > MissingCaptureLimit ||
		(state.LastCaptureID == state.ActivationCaptureID) != state.LastReceivedAt.IsZero() {
		return fmt.Errorf("invalid producer state/checkpoint")
	}
	if state.LatestSeenAt.IsZero() {
		if state.Open != nil || !state.ClosedThrough.IsZero() || state.MissingCaptures != 0 {
			return fmt.Errorf("producer state has no seen timestamp")
		}
		return nil
	}
	if state.LastCaptureID == state.ActivationCaptureID || state.ClosedThrough.After(state.LatestSeenAt) {
		return fmt.Errorf("invalid producer watermark")
	}
	if state.Open == nil {
		if !state.ClosedThrough.Equal(state.LatestSeenAt) || state.MissingCaptures != MissingCaptureLimit {
			return fmt.Errorf("producer state lost open sequence")
		}
		return nil
	}
	open := state.Open
	fingerprint, err := hex.DecodeString(open.EvidenceFingerprint)
	if err != nil || len(fingerprint) != 32 || open.Observations <= 0 || len(open.Layouts) == 0 || len(open.Scopes) == 0 ||
		len(open.Layouts) > open.Observations || len(open.Scopes) > open.Observations ||
		open.FirstCaptureID <= state.ActivationCaptureID || open.LastCaptureID < open.FirstCaptureID || open.LastCaptureID > state.LastCaptureID ||
		open.EarliestAt.IsZero() || open.EarliestAt.After(open.LatestAt) || !open.LatestAt.Equal(state.LatestSeenAt) ||
		(!state.ClosedThrough.IsZero() && !open.EarliestAt.After(state.ClosedThrough)) || state.MissingCaptures == MissingCaptureLimit {
		return fmt.Errorf("invalid producer open descriptor")
	}
	for i, reference := range open.Layouts {
		layout, err := NewLayout(reference.Version, reference.LengthMeters, reference.NominalMeters)
		if err != nil || reference.ID <= 0 || reference.LengthMeters != layout.length || reference.NominalMeters != meters(state.Model.NominalMeters) ||
			(i > 0 && open.Layouts[i-1].ID >= reference.ID) {
			return fmt.Errorf("invalid producer layout reference")
		}
	}
	for i, scope := range open.Scopes {
		if err := scope.validate(); err != nil {
			return err
		}
		if i > 0 && !producerScopeLess(open.Scopes[i-1], scope) {
			return fmt.Errorf("invalid producer scope references")
		}
		found := false
		for _, reference := range open.Layouts {
			if reference.Version == scope.Version {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("producer scope has no layout reference")
		}
	}
	return nil
}

func sortProducerReferences(references []ProducerLayout) {
	sort.Slice(references, func(i, j int) bool { return references[i].ID < references[j].ID })
}
