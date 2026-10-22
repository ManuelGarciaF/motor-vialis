package bottlenecks

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func producerModel() PersistenceModel {
	return PersistenceModel{Name: "experimental", MeasurementVersion: "commercial-v1", MatcherVersion: "conservative-v1",
		NominalMeters: 500, Policy: testPolicy(), TimeZone: "America/Argentina/Buenos_Aires", RawRetentionDays: 7, ClosureCaptures: 4}
}

func producerState(t *testing.T) ProducerState {
	t.Helper()
	state, err := NewProducerState(producerModel(), "v", 10)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func producerRow(id int64, seconds int, progress ...float64) ProducerObservation {
	row := ProducerObservation{CaptureID: id, LayoutID: 1, Candidate: CandidateObservation{VehicleID: "v",
		Scope: Scope{ProviderRouteID: "g", Version: testVersion()}, ObservedAt: epoch.Add(time.Duration(seconds) * time.Second)}}
	for _, p := range progress {
		row.Candidate.Candidates = append(row.Candidate.Candidates, Projection{ProgressMeters: p})
	}
	return row
}

func producerCapture(id int64, rows ...ProducerObservation) ProducerCapture {
	return ProducerCapture{ID: id, ReceivedAt: epoch.Add(time.Duration(id) * time.Minute), Successful: true, New: rows}
}

func applyProducer(t *testing.T, state ProducerState, input ProducerCapture, layouts ...PersistedLayout) (ProducerState, ProducerResult) {
	t.Helper()
	if len(layouts) == 0 {
		layouts = []PersistedLayout{{ID: 1, Layout: testLayout(t, 1500, 500)}}
	}
	next, result, err := ProduceCapture(state, input, layouts)
	if err != nil {
		t.Fatal(err)
	}
	return next, result
}

func restoreProducer(t *testing.T, state ProducerState) ProducerState {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored ProducerState
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err = restored.Validate(); err != nil {
		t.Fatal(err)
	}
	return restored
}

func closeProducer(t *testing.T, state ProducerState, replay []ProducerObservation, layouts ...PersistedLayout) (ProducerState, ProducerResult) {
	t.Helper()
	for i := state.MissingCaptures; i < MissingCaptureLimit; i++ {
		input := producerCapture(state.LastCaptureID + 1)
		if i == MissingCaptureLimit-1 {
			input.Replay = replay
		}
		var result ProducerResult
		state, result = applyProducer(t, state, input, layouts...)
		if i == MissingCaptureLimit-1 {
			return state, result
		}
		if len(result.Segments) != 0 || result.Closed {
			t.Fatal("measurement published before closure")
		}
		state = restoreProducer(t, state)
	}
	t.Fatal("state was already closed")
	return state, ProducerResult{}
}

func expectedProducerMeasurement(t *testing.T, layout Layout, rows []ProducerObservation, reason Rejection) Measurements {
	t.Helper()
	candidates := make([]CandidateObservation, len(rows))
	for i, row := range rows {
		candidates[i] = row.Candidate
	}
	matched, err := MatchConservatively(layout, candidates, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	measured, err := Measure(layout, matched.Observations, testPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for i := range measured.Incomplete {
		if measured.Incomplete[i].Reason == BatchBoundary {
			measured.Incomplete[i].Reason = reason
		}
	}
	if reason == ScopeChanged {
		measured.Rejections[reason]++
	}
	return measured
}

func TestProducerDefersOriginalCandidateMatchingUntilClose(t *testing.T) {
	state := producerState(t)
	first := producerRow(11, 0, 250)
	ambiguous := producerRow(12, 60, 750, 900)
	state, result := applyProducer(t, state, producerCapture(11, first))
	state, result = applyProducer(t, state, producerCapture(12, ambiguous))
	if result.Closed || len(result.Segments) != 0 || state.MissingCaptures != 0 {
		t.Fatal("provisional matching emitted final evidence or counted absence")
	}
	state = restoreProducer(t, state)
	last := producerRow(13, 120, 850)
	state, _ = applyProducer(t, state, producerCapture(13, last))
	before, err := json.Marshal([]ProducerObservation{first, ambiguous, last})
	if err != nil {
		t.Fatal(err)
	}
	state, result = closeProducer(t, state, []ProducerObservation{last, first, ambiguous})
	if !result.Closed || state.Open != nil || !state.ClosedThrough.Equal(last.Candidate.ObservedAt) || len(result.Segments) != 1 || result.Segments[0].TemporallyResolved != 1 {
		t.Fatalf("closed replay did not resolve original alternatives: %+v %+v", state, result)
	}
	want := expectedProducerMeasurement(t, testLayout(t, 1500, 500), []ProducerObservation{first, ambiguous, last}, MissingUpdates)
	if !reflect.DeepEqual(result.Segments[0].Measurements, want) {
		t.Fatalf("batch replay mismatch: %+v vs %+v", result.Segments[0].Measurements, want)
	}
	after, err := json.Marshal([]ProducerObservation{first, ambiguous, last})
	if err != nil || string(before) != string(after) {
		t.Fatal("original candidate arrays were mutated")
	}
}

func TestProducerLateOpenInsertionReordersScopeTimeline(t *testing.T) {
	state := producerState(t)
	a := producerRow(11, 0, 250)
	b := producerRow(11, 120, 1250)
	state, _ = applyProducer(t, state, producerCapture(11, a, b))
	late := producerRow(12, 60, 750)
	late.Candidate.Scope.ProviderRouteID = "other"
	state, result := applyProducer(t, state, producerCapture(12, late))
	if state.MissingCaptures != 1 || state.Open.Observations != 3 || result.LateObservations != 0 {
		t.Fatal("late OPEN evidence was discarded or falsely reset absence")
	}
	state, result = closeProducer(t, state, []ProducerObservation{a, b, late})
	if len(result.Segments) != 3 || len(result.Segments[0].Measurements.Passages) != 0 || len(result.Segments[2].Measurements.Passages) != 0 {
		t.Fatalf("A->B->A bridged after late insertion: %+v", result)
	}
	if !state.ClosedThrough.Equal(b.Candidate.ObservedAt) {
		t.Fatal("closed watermark followed receipt rather than GPS order")
	}
}

func TestProducerScopeAndLayoutBarriersHaveFinalReasons(t *testing.T) {
	state := producerState(t)
	a := []ProducerObservation{producerRow(11, 0, 250), producerRow(11, 60, 750)}
	version := testVersion()
	version.ShapeHash = "different"
	otherLayout, err := NewLayout(version, 1500, 500)
	if err != nil {
		t.Fatal(err)
	}
	b := producerRow(12, 120, 750)
	b.LayoutID = 2
	b.Candidate.Scope.Version = version
	c := producerRow(13, 180, 750)
	rows := append(append([]ProducerObservation{}, a...), b, c)
	layouts := []PersistedLayout{{ID: 1, Layout: testLayout(t, 1500, 500)}, {ID: 2, Layout: otherLayout}}
	state, _ = applyProducer(t, state, producerCapture(11, a...), layouts...)
	state, _ = applyProducer(t, state, producerCapture(12, b), layouts...)
	state, _ = applyProducer(t, state, producerCapture(13, c), layouts...)
	_, result := closeProducer(t, state, rows, layouts...)
	if len(result.Segments) != 3 || len(result.Segments[0].Measurements.Incomplete) != 1 || result.Segments[0].Measurements.Incomplete[0].Reason != ScopeChanged {
		t.Fatalf("version boundary lost supported incomplete entry: %+v", result)
	}
	for _, segment := range result.Segments {
		for _, incomplete := range segment.Measurements.Incomplete {
			if incomplete.Reason == BatchBoundary {
				t.Fatal("technical batch censoring escaped final producer")
			}
		}
	}
}

func TestProducerReceptionCountsActualCapturesAndNewerTimestamps(t *testing.T) {
	state := producerState(t)
	first := producerRow(11, 0)
	state, _ = applyProducer(t, state, producerCapture(11, first))
	state, _ = applyProducer(t, state, producerCapture(12))
	partial := producerCapture(13)
	partial.Partial = true
	state, _ = applyProducer(t, state, partial)
	failed := producerCapture(14)
	failed.Successful = false
	state, _ = applyProducer(t, state, failed)
	if state.MissingCaptures != 1 {
		t.Fatal("failed/partial captures counted as absence")
	}
	stationary := producerRow(15, 60, 250)
	partial = producerCapture(15, stationary)
	partial.Partial = true
	state, _ = applyProducer(t, state, partial)
	if state.MissingCaptures != 0 || !state.LatestSeenAt.Equal(stationary.Candidate.ObservedAt) {
		t.Fatal("partial valid update ignored")
	}
	for id := int64(16); id <= 18; id++ {
		state, _ = applyProducer(t, state, producerCapture(id))
	}
	unmatched := producerRow(19, 120)
	state, result := applyProducer(t, state, producerCapture(19, unmatched))
	if state.MissingCaptures != 0 || result.Closed {
		t.Fatal("unmatched new timestamp on fourth capture did not reset absence")
	}
	_, result = closeProducer(t, state, []ProducerObservation{first, stationary, unmatched})
	if !result.Closed || result.Segments[0].Unmatched != 2 {
		t.Fatalf("unmatched evidence lost: %+v", result)
	}
}

func TestProducerCloseOnceAndNeverReopenClosedEvidence(t *testing.T) {
	state := producerState(t)
	first, last := producerRow(11, 0, 250), producerRow(11, 120, 1250)
	state, _ = applyProducer(t, state, producerCapture(11, first, last))
	state, closed := closeProducer(t, state, []ProducerObservation{first, last})
	if len(closed.Segments[0].Measurements.Passages) != 1 {
		t.Fatal("valid final passage lost")
	}
	state = restoreProducer(t, state)
	state, result := applyProducer(t, state, producerCapture(16))
	if result.Closed || len(result.Segments) != 0 || state.MissingCaptures != 4 {
		t.Fatal("closure repeated")
	}
	old := producerRow(17, 60, 750)
	state, result = applyProducer(t, state, producerCapture(17, old))
	if result.LateObservations != 1 || state.Open != nil {
		t.Fatal("older GPS reopened finalized sequence")
	}
	newer := producerRow(18, 180, 1250)
	state, _ = applyProducer(t, state, producerCapture(18, newer))
	old.CaptureID = 19
	state, result = applyProducer(t, state, producerCapture(19, old))
	if result.LateObservations != 1 || state.Open.Observations != 1 {
		t.Fatal("old GPS joined later open sequence")
	}
	_, result = closeProducer(t, state, []ProducerObservation{newer})
	if len(result.Segments[0].Measurements.Passages) != 0 {
		t.Fatal("later sequence completed previous pending entry")
	}
}

func TestProducerMalformedReplayAndInputsAreAtomic(t *testing.T) {
	state := producerState(t)
	rows := []ProducerObservation{producerRow(11, 0, 250), producerRow(11, 120, 1250)}
	state, _ = applyProducer(t, state, producerCapture(11, rows...))
	for id := int64(12); id <= 14; id++ {
		state, _ = applyProducer(t, state, producerCapture(id))
	}
	layouts := []PersistedLayout{{ID: 1, Layout: testLayout(t, 1500, 500)}}
	for _, tt := range []struct {
		name string
		edit func(*ProducerCapture)
	}{
		{"missing replay", func(c *ProducerCapture) { c.Replay = nil }},
		{"duplicate replay", func(c *ProducerCapture) { c.Replay = append(c.Replay, c.Replay[0]) }},
		{"changed candidate", func(c *ProducerCapture) { c.Replay[0].Candidate.Candidates = []Projection{{ProgressMeters: 200}} }},
		{"wrong receipt", func(c *ProducerCapture) { c.Replay[0].CaptureID = 9 }},
		{"future receipt", func(c *ProducerCapture) { c.Replay[0].CaptureID = 20 }},
		{"wrong layout", func(c *ProducerCapture) { c.Replay[0].LayoutID = 2 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := producerCapture(15)
			input.Replay = append([]ProducerObservation(nil), rows...)
			tt.edit(&input)
			before, _ := json.Marshal(state)
			next, result, err := ProduceCapture(state, input, layouts)
			after, _ := json.Marshal(state)
			if err == nil || !reflect.DeepEqual(next, state) || !reflect.DeepEqual(result, ProducerResult{}) || string(before) != string(after) {
				t.Fatalf("invalid replay partially advanced: %v %+v", err, result)
			}
		})
	}
	_, _, err := ProduceCapture(state, producerCapture(11), layouts)
	if !errors.Is(err, ErrCaptureAlreadyProcessed) {
		t.Fatalf("historical replay classification: %v", err)
	}
}
