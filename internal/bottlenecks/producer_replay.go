package bottlenecks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
)

// Canonicalization copies candidate arrays because matching prunes alternatives
// in place internally. Neither hashing nor later replay may reuse that pruning.
func canonicalProducerRows(input []ProducerObservation, vehicle string, layouts map[int64]Layout) ([]ProducerObservation, int, error) {
	rows := append([]ProducerObservation(nil), input...)
	for i := range rows {
		row := &rows[i]
		candidate := &row.Candidate
		layout, exists := layouts[row.LayoutID]
		if !exists || row.CaptureID <= 0 || candidate.VehicleID != vehicle || candidate.ObservedAt.IsZero() || candidate.Scope.Version != layout.version {
			return nil, 0, fmt.Errorf("invalid producer row identity or missing exact layout")
		}
		if err := candidate.Scope.validate(); err != nil {
			return nil, 0, err
		}
		candidate.ObservedAt = candidate.ObservedAt.UTC()
		candidate.Candidates = append([]Projection{}, candidate.Candidates...)
		for _, projection := range candidate.Candidates {
			if !finite(projection.ProgressMeters) || !finite(projection.DistanceMeters) || projection.DistanceMeters < 0 {
				return nil, 0, fmt.Errorf("invalid producer projection")
			}
		}
		sort.Slice(candidate.Candidates, func(i, j int) bool {
			a, b := candidate.Candidates[i], candidate.Candidates[j]
			if a.ProgressMeters != b.ProgressMeters {
				return a.ProgressMeters < b.ProgressMeters
			}
			return a.DistanceMeters < b.DistanceMeters
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Candidate.ObservedAt.Before(rows[j].Candidate.ObservedAt) })
	unique := rows[:0]
	duplicates := 0
	for _, row := range rows {
		if len(unique) > 0 && unique[len(unique)-1].Candidate.ObservedAt.Equal(row.Candidate.ObservedAt) {
			if !reflect.DeepEqual(unique[len(unique)-1], row) {
				return nil, 0, fmt.Errorf("conflicting canonical producer timestamp")
			}
			duplicates++
			continue
		}
		unique = append(unique, row)
	}
	return unique, duplicates, nil
}

func accumulateProducerEvidence(open *OpenProducerSequence, row ProducerObservation, layout Layout) error {
	if open.Observations == math.MaxInt {
		return fmt.Errorf("producer observation counter exhausted")
	}
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	fingerprint, err := hex.DecodeString(open.EvidenceFingerprint)
	if err != nil || len(fingerprint) != len(digest) {
		return fmt.Errorf("invalid producer evidence fingerprint")
	}
	// An order-independent digest permits late insertion without persisting a
	// candidate history. Exact row count and uniqueness are checked on replay.
	for i := range digest {
		fingerprint[i] ^= digest[i]
	}
	open.EvidenceFingerprint = hex.EncodeToString(fingerprint)
	open.Observations++
	open.FirstCaptureID = min(open.FirstCaptureID, row.CaptureID)
	open.LastCaptureID = max(open.LastCaptureID, row.CaptureID)
	if row.Candidate.ObservedAt.Before(open.EarliestAt) {
		open.EarliestAt = row.Candidate.ObservedAt
	}
	if row.Candidate.ObservedAt.After(open.LatestAt) {
		open.LatestAt = row.Candidate.ObservedAt
	}
	scopeFound := false
	for _, existing := range open.Scopes {
		if existing == row.Candidate.Scope {
			scopeFound = true
			break
		}
	}
	if !scopeFound {
		open.Scopes = append(open.Scopes, row.Candidate.Scope)
		sort.Slice(open.Scopes, func(i, j int) bool { return producerScopeLess(open.Scopes[i], open.Scopes[j]) })
	}

	reference := producerLayoutReference(row.LayoutID, layout)
	for _, existing := range open.Layouts {
		if existing.ID == reference.ID {
			if existing != reference {
				return fmt.Errorf("producer layout changed during open sequence")
			}
			return nil
		}
	}
	open.Layouts = append(open.Layouts, reference)
	sortProducerReferences(open.Layouts)
	return nil
}

func producerScopeLess(a, b Scope) bool {
	left := [...]string{a.ProviderRouteID, a.Version.RouteID, a.Version.ShapeID, a.Version.ShapeHash}
	right := [...]string{b.ProviderRouteID, b.Version.RouteID, b.Version.ShapeID, b.Version.ShapeHash}
	for i := range left {
		if left[i] != right[i] {
			return left[i] < right[i]
		}
	}
	if a.ProviderDirection != b.ProviderDirection {
		return a.ProviderDirection < b.ProviderDirection
	}
	return a.Version.Direction < b.Version.Direction
}

func finalizeProducerSequence(state ProducerState, capture ProducerCapture, layouts map[int64]Layout) ([]ProducedSegment, error) {
	rows, duplicates, err := canonicalProducerRows(capture.Replay, state.VehicleID, layouts)
	if err != nil {
		return nil, err
	}
	if duplicates != 0 || len(rows) != state.Open.Observations {
		return nil, fmt.Errorf("producer replay is incomplete or duplicated")
	}
	expected := *state.Open
	replayed := OpenProducerSequence{FirstCaptureID: expected.FirstCaptureID, LastCaptureID: expected.FirstCaptureID,
		EarliestAt: rows[0].Candidate.ObservedAt, LatestAt: rows[0].Candidate.ObservedAt,
		EvidenceFingerprint: hex.EncodeToString(make([]byte, sha256.Size)), Layouts: []ProducerLayout{}}
	for _, row := range rows {
		if row.CaptureID < expected.FirstCaptureID || row.CaptureID > capture.ID || row.CaptureID <= state.ActivationCaptureID ||
			(!state.ClosedThrough.IsZero() && !row.Candidate.ObservedAt.After(state.ClosedThrough)) {
			return nil, fmt.Errorf("producer replay is outside accepted receipt/GPS bounds")
		}
		if err = accumulateProducerEvidence(&replayed, row, layouts[row.LayoutID]); err != nil {
			return nil, err
		}
	}
	if !reflect.DeepEqual(replayed, expected) {
		return nil, fmt.Errorf("producer replay differs from accepted evidence")
	}

	segments := make([]ProducedSegment, 0)
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && rows[end].LayoutID == rows[start].LayoutID && rows[end].Candidate.Scope == rows[start].Candidate.Scope {
			end++
		}
		layout := layouts[rows[start].LayoutID]
		candidates := make([]CandidateObservation, end-start)
		for i, row := range rows[start:end] {
			candidates[i] = row.Candidate
		}
		matching, err := MatchConservatively(layout, candidates, state.Model.Policy)
		if err != nil {
			return nil, err
		}
		measured, err := Measure(layout, matching.Observations, state.Model.Policy)
		if err != nil {
			return nil, err
		}
		reason := MissingUpdates
		if end < len(rows) {
			reason = ScopeChanged
			measured.Rejections[ScopeChanged]++
		}
		for i := range measured.Incomplete {
			if measured.Incomplete[i].Reason == BatchBoundary {
				measured.Incomplete[i].Reason = reason
			}
		}
		segments = append(segments, ProducedSegment{LayoutID: rows[start].LayoutID, Scope: rows[start].Candidate.Scope,
			Measurements: measured, SpatiallyUnique: matching.SpatiallyUnique, TemporallyResolved: matching.TemporallyResolved,
			Ambiguous: matching.Ambiguous, Unmatched: matching.Unmatched})
		start = end
	}
	return segments, nil
}
