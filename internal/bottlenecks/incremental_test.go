package bottlenecks

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func captureState(t *testing.T, l Layout) CaptureState {
	t.Helper()
	s, err := NewCaptureState(l, testPolicy(), "v", 10)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func capture(id int64, rows ...Observation) CaptureInput {
	return CaptureInput{ID: id, ReceivedAt: epoch.Add(time.Duration(id) * time.Minute), Successful: true, Observations: rows}
}

func applyCapture(t *testing.T, l Layout, s CaptureState, in CaptureInput) (CaptureState, CaptureMeasurement) {
	t.Helper()
	next, result, err := MeasureCapture(l, s, in)
	if err != nil {
		t.Fatal(err)
	}
	return next, result
}

func roundTripState(t *testing.T, s CaptureState) CaptureState {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var restored CaptureState
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	return restored
}

func TestIncrementalPreservesEntryAcrossCapturesAndRestart(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	s, r := applyCapture(t, l, s, capture(11, observation(0, 250), observation(60, 750)))
	if len(r.Passages) != 0 || len(r.Incomplete) != 0 || s.Pending == nil {
		t.Fatalf("lost pending entry: %+v %+v", s, r)
	}
	s = roundTripState(t, s)
	s, r = applyCapture(t, l, s, capture(12, observation(120, 750), observation(180, 1250)))
	expected := measure(t, l, observation(0, 250), observation(60, 750), observation(120, 750), observation(180, 1250))
	if !reflect.DeepEqual(r.Passages, expected.Passages) || len(r.Incomplete) != 0 || s.Pending == nil {
		t.Fatalf("batch/incremental mismatch: %+v %+v", r, expected)
	}
}

func TestIncrementalFourCapturesCloseOnceWithoutExtendingDuration(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	s, _ = applyCapture(t, l, s, capture(11, observation(0, 250), observation(60, 750)))
	for id := int64(12); id <= 16; id++ {
		var r CaptureMeasurement
		s, r = applyCapture(t, l, s, capture(id))
		if id == 15 {
			if !r.Closed || len(r.Incomplete) != 1 || r.Incomplete[0].Reason != MissingUpdates || len(r.Passages) != 0 || s.Previous != nil || s.Pending != nil {
				t.Fatalf("wrong close: %+v %+v", s, r)
			}
		} else if r.Closed || len(r.Incomplete) != 0 {
			t.Fatalf("unexpected closure at %d: %+v", id, r)
		}
	}
	if s.MissingCaptures != 4 {
		t.Fatal("counter did not saturate")
	}
	// Older evidence cannot reopen a closed entry or alter its quality counters.
	s, late := applyCapture(t, l, s, capture(17, observation(30, 500)))
	if late.LateObservations != 1 || len(late.Passages) != 0 || late.Closed {
		t.Fatalf("late evidence reopened entry: %+v", late)
	}
	// A new timestamp starts independently; it cannot complete the closed entry.
	s, r := applyCapture(t, l, s, capture(18, observation(120, 1250)))
	if len(r.Passages) != 0 || s.MissingCaptures != 0 || s.Previous == nil {
		t.Fatalf("reopened closed entry: %+v", r)
	}
}

func TestIncrementalPartialAndFailedCapturesDoNotCount(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	s, _ = applyCapture(t, l, s, capture(11, observation(0, 250), observation(60, 750)))
	s, _ = applyCapture(t, l, s, capture(12))
	for id := int64(13); id <= 18; id++ {
		in := capture(id)
		if id%2 == 0 {
			in.Partial = true
		} else {
			in.Successful = false
		}
		s, _ = applyCapture(t, l, s, in)
		if s.MissingCaptures != 1 {
			t.Fatalf("ineligible absence counted: %+v", s)
		}
	}
	// Valid observations in a partial capture are still processed. Same position
	// with a newer GPS timestamp resets absence and contributes stopped time.
	in := capture(19, observation(120, 750))
	in.Partial = true
	s, r := applyCapture(t, l, s, in)
	if s.MissingCaptures != 0 || s.Pending.InteriorObservations != 2 || len(r.Passages) != 0 {
		t.Fatalf("partial update ignored: %+v", s)
	}
}

func TestIncrementalProcessesUpdateBeforeFourthAbsence(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	s, _ = applyCapture(t, l, s, capture(11, observation(0, 250), observation(60, 750)))
	for id := int64(12); id <= 14; id++ {
		s, _ = applyCapture(t, l, s, capture(id))
	}
	s, r := applyCapture(t, l, s, capture(15, observation(120, 1250)))
	if len(r.Passages) != 1 || r.Closed || s.MissingCaptures != 0 {
		t.Fatalf("closed before updates: %+v", r)
	}
}

func TestIncrementalReplayLateAndConflictsAreAtomic(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	s, _ = applyCapture(t, l, s, capture(11, observation(0, 250), observation(60, 750)))
	before := cloneCaptureState(s)
	next, r, err := MeasureCapture(l, s, capture(11, observation(120, 1250)))
	if !errors.Is(err, ErrCaptureAlreadyProcessed) || !reflect.DeepEqual(s, next) || len(r.Passages) != 0 {
		t.Fatal("replay emitted a transition")
	}
	_, _, err = MeasureCapture(l, s, capture(12, observation(30, 500)))
	if !errors.Is(err, ErrObservationBeforeWatermark) {
		t.Fatal("out-of-order active evidence was silently discarded")
	}
	s, r = applyCapture(t, l, s, capture(12, observation(60, 750)))
	if r.LateObservations != 0 || r.Duplicates != 1 || s.MissingCaptures != 1 {
		t.Fatalf("incorrect duplicate/late handling: %+v", r)
	}
	// The new row would complete a passage, but conflicting evidence rejects the
	// entire capture, including all generated results and checkpoint changes.
	conflict := observation(120, 1250)
	other := conflict
	other.ProgressMeters = 1200
	next, r, err = MeasureCapture(l, s, capture(13, conflict, other))
	if err == nil || !reflect.DeepEqual(next, s) || len(r.Passages) != 0 {
		t.Fatal("partial transition escaped")
	}
	if !reflect.DeepEqual(before.Pending.Entered, s.Pending.Entered) || before.Pending.InteriorObservations != s.Pending.InteriorObservations {
		t.Fatal("input pointers mutated")
	}
	conflict = observation(60, 800)
	_, _, err = MeasureCapture(l, s, capture(13, conflict))
	if err == nil {
		t.Fatal("watermark conflict accepted")
	}
}

func TestIncrementalReplayAfterRestoringNewerCheckpoint(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	historical := capture(11, observation(0, 250), observation(60, 750))
	latest := capture(12, observation(120, 1250))
	s, _ = applyCapture(t, l, s, historical)
	s, _ = applyCapture(t, l, s, latest)
	s = roundTripState(t, s)

	for _, tt := range []struct {
		name  string
		input CaptureInput
	}{
		{"historical", historical},
		{"latest", latest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := cloneCaptureState(s)
			next, result, err := MeasureCapture(l, s, tt.input)
			if !errors.Is(err, ErrCaptureAlreadyProcessed) {
				t.Fatalf("replay classification: got %v, want %v", err, ErrCaptureAlreadyProcessed)
			}
			if !reflect.DeepEqual(next, before) || !reflect.DeepEqual(s, before) {
				t.Fatal("replay changed state or checkpoint")
			}
			if !reflect.DeepEqual(result, CaptureMeasurement{}) {
				t.Fatalf("replay emitted results: %+v", result)
			}
		})
	}
}

func TestIncrementalReplayOrderingPreservesMetadataAndStateValidation(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	s, _ = applyCapture(t, l, s, capture(11, observation(0, 250), observation(60, 750)))
	s, _ = applyCapture(t, l, s, capture(12))
	s = roundTripState(t, s)

	for _, tt := range []struct {
		name string
		edit func(*CaptureState, *CaptureInput)
	}{
		{"new capture with old receipt", func(_ *CaptureState, in *CaptureInput) { in.ReceivedAt = capture(11).ReceivedAt }},
		{"new capture with zero receipt", func(_ *CaptureState, in *CaptureInput) { in.ReceivedAt = time.Time{} }},
		{"zero ID", func(_ *CaptureState, in *CaptureInput) { in.ID = 0 }},
		{"negative ID", func(_ *CaptureState, in *CaptureInput) { in.ID = -1 }},
		{"replay with invalid restored state", func(state *CaptureState, in *CaptureInput) {
			state.Policy.MaximumGap = 0
			*in = capture(11)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := cloneCaptureState(s)
			input := capture(13)
			tt.edit(&state, &input)
			before := cloneCaptureState(state)
			next, result, err := MeasureCapture(l, state, input)
			if err == nil || errors.Is(err, ErrCaptureAlreadyProcessed) {
				t.Fatalf("invalid metadata/state misclassified: %v", err)
			}
			if !reflect.DeepEqual(next, before) || !reflect.DeepEqual(state, before) {
				t.Fatal("invalid capture changed state or checkpoint")
			}
			if !reflect.DeepEqual(result, CaptureMeasurement{}) {
				t.Fatalf("invalid capture emitted results: %+v", result)
			}
		})
	}
}

func TestIncrementalNoExtrapolationAndGapStillApplies(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	for id := int64(11); id <= 16; id++ {
		var r CaptureMeasurement
		s, r = applyCapture(t, l, s, capture(id))
		if r.Closed || s.MissingCaptures != 0 {
			t.Fatal("inferred a vehicle without evidence")
		}
	}
	s, _ = applyCapture(t, l, s, capture(17, observation(0, 250), observation(60, 750)))
	_, r := applyCapture(t, l, s, capture(18, observation(241, 1250)))
	if len(r.Passages) != 0 || len(r.Incomplete) != 1 || r.Incomplete[0].Reason != GPSGap {
		t.Fatalf("capture closure relaxed GPS gap: %+v", r)
	}
}

func TestIncrementalScopeChangeBreaksContinuity(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	s, _ = applyCapture(t, l, s, capture(11, observation(0, 250), observation(60, 750)))
	changed := observation(120, 1250)
	changed.Scope.ProviderRouteID = "another"
	_, r := applyCapture(t, l, s, capture(12, changed))
	if len(r.Passages) != 0 || len(r.Incomplete) != 1 || r.Incomplete[0].Reason != ScopeChanged {
		t.Fatalf("mixed provider identity: %+v", r)
	}
}

func TestIncrementalValidatesRestoredStateAndMetadata(t *testing.T) {
	l := testLayout(t, 1500, 500)
	s := captureState(t, l)
	s, _ = applyCapture(t, l, s, capture(11, observation(0, 250), observation(60, 750)))
	tests := []struct {
		name string
		edit func(*CaptureState, *CaptureInput)
	}{
		{"policy", func(s *CaptureState, _ *CaptureInput) { s.Policy.MaximumGap = 0 }},
		{"layout", func(s *CaptureState, _ *CaptureInput) { s.NominalMeters = 300 }},
		{"missing watermark", func(s *CaptureState, _ *CaptureInput) { s.Watermark = nil }},
		{"closed continuity", func(s *CaptureState, _ *CaptureInput) { s.MissingCaptures = 4 }},
		{"pending", func(s *CaptureState, _ *CaptureInput) { s.Pending.Sector.Index = 100 }},
		{"capture order", func(_ *CaptureState, in *CaptureInput) { in.ReceivedAt = epoch }},
		{"failed rows", func(_ *CaptureState, in *CaptureInput) {
			in.Successful = false
			in.Observations = []Observation{observation(120, 1250)}
		}},
		{"vehicle", func(_ *CaptureState, in *CaptureInput) {
			o := observation(120, 1250)
			o.VehicleID = "other"
			in.Observations = []Observation{o}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := cloneCaptureState(s)
			in := capture(12)
			tt.edit(&state, &in)
			if _, _, err := MeasureCapture(l, state, in); err == nil {
				t.Fatal("invalid transition accepted")
			}
		})
	}
}

func TestIncrementalBatchParityAcrossEveryPartition(t *testing.T) {
	l := testLayout(t, 1500, 500)
	rows := []Observation{observation(0, 0), observation(30, 250), observation(60, 500), observation(90, 500), observation(120, 750), observation(150, 1000), observation(180, 1250), observation(210, 1500)}
	expected := measure(t, l, rows...)
	for mask := 0; mask < 1<<(len(rows)-1); mask++ {
		s := captureState(t, l)
		passages := []Passage{}
		start := 0
		id := int64(11)
		for end := 1; end <= len(rows); end++ {
			if end < len(rows) && mask&(1<<(end-1)) == 0 {
				continue
			}
			var r CaptureMeasurement
			s, r = applyCapture(t, l, s, capture(id, rows[start:end]...))
			if len(r.Incomplete) != 0 {
				t.Fatalf("technical boundary censored mask=%d", mask)
			}
			passages = append(passages, r.Passages...)
			s = roundTripState(t, s)
			start = end
			id++
		}
		if !reflect.DeepEqual(passages, expected.Passages) {
			t.Fatalf("partition %d differs from batch", mask)
		}
	}
}
