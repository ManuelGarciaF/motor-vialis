package bottlenecks

import (
	"reflect"
	"testing"
	"time"
)

func TestProducerClosedBatchParityAcrossPartitionsAndRestarts(t *testing.T) {
	layout := testLayout(t, 1500, 500)
	cases := []struct {
		name string
		rows []ProducerObservation
	}{
		{"boundaries and dwell", []ProducerObservation{producerRow(0, 0, 0), producerRow(0, 30, 250), producerRow(0, 60, 500), producerRow(0, 90, 500), producerRow(0, 120, 750), producerRow(0, 150, 1000), producerRow(0, 180, 1250), producerRow(0, 210, 1500)}},
		{"backward anchor resolution", []ProducerObservation{producerRow(0, 0, 0, 500), producerRow(0, 60, 250)}},
		{"contradictory late right anchor", []ProducerObservation{producerRow(0, 0, 250), producerRow(0, 60, 750, 100), producerRow(0, 120, 700)}},
		{"GPS gap", []ProducerObservation{producerRow(0, 0, 250), producerRow(0, 60, 750), producerRow(0, 241, 1250)}},
		{"unmatched barrier", []ProducerObservation{producerRow(0, 0, 250), producerRow(0, 60, 750), producerRow(0, 90), producerRow(0, 120, 1250)}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			for mask := 0; mask < 1<<(len(tt.rows)-1); mask++ {
				state := producerState(t)
				accepted := []ProducerObservation{}
				start := 0
				id := int64(11)
				for end := 1; end <= len(tt.rows); end++ {
					if end < len(tt.rows) && mask&(1<<(end-1)) == 0 {
						continue
					}
					rows := append([]ProducerObservation(nil), tt.rows[start:end]...)
					for i := range rows {
						rows[i].CaptureID = id
					}
					var result ProducerResult
					state, result = applyProducer(t, state, producerCapture(id, rows...))
					if result.Closed || len(result.Segments) != 0 {
						t.Fatalf("mask %d finalized a technical prefix", mask)
					}
					state = restoreProducer(t, state)
					accepted = append(accepted, rows...)
					start, id = end, id+1
				}
				_, result := closeProducer(t, state, accepted)
				want := expectedProducerMeasurement(t, layout, accepted, MissingUpdates)
				if len(result.Segments) != 1 || !reflect.DeepEqual(result.Segments[0].Measurements, want) {
					t.Fatalf("mask %d differs from closed batch: %+v vs %+v", mask, result.Segments, want)
				}
			}
		})
	}
}

func TestProducerOlderOpenInsertionChangesInterpolationAndInteriorSupport(t *testing.T) {
	state := producerState(t)
	first, last := producerRow(11, 0, 250), producerRow(11, 120, 1250)
	state, _ = applyProducer(t, state, producerCapture(11, first, last))
	interior := producerRow(12, 90, 750)
	state, _ = applyProducer(t, state, producerCapture(12, interior))
	_, result := closeProducer(t, state, []ProducerObservation{last, interior, first})
	passage := result.Segments[0].Measurements.Passages[0]
	if passage.InteriorObservations != 1 || passage.SameGPSInterval || passage.Duration.Seconds() != 60 || !passage.Entered.At.Equal(epoch.Add(45*time.Second)) {
		t.Fatalf("late OPEN evidence was not used to recompute support: %+v", passage)
	}
}

func TestProducerWrongRestoredStateAndLayoutAreRejected(t *testing.T) {
	state := producerState(t)
	row := producerRow(11, 0, 250)
	state, _ = applyProducer(t, state, producerCapture(11, row))
	for _, tt := range []struct {
		name string
		edit func(*ProducerState)
	}{
		{"invalid model", func(s *ProducerState) { s.Model.ClosureCaptures = 3 }},
		{"lost descriptor", func(s *ProducerState) { s.Open = nil }},
		{"invalid count", func(s *ProducerState) { s.Open.Observations = 0 }},
		{"invalid fingerprint", func(s *ProducerState) { s.Open.EvidenceFingerprint = "invalid" }},
		{"preactivation bounds", func(s *ProducerState) { s.Open.FirstCaptureID = 10 }},
		{"future bounds", func(s *ProducerState) { s.Open.LastCaptureID = 12 }},
		{"invalid layout", func(s *ProducerState) { s.Open.Layouts[0].NominalMeters = 800 }},
		{"invalid watermark", func(s *ProducerState) { s.ClosedThrough = s.LatestSeenAt.Add(1) }},
		{"closed counter", func(s *ProducerState) { s.MissingCaptures = 4 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bad := cloneProducerState(state)
			tt.edit(&bad)
			if bad.Validate() == nil {
				t.Fatal("malformed restart state accepted")
			}
		})
	}
	layout := testLayout(t, 1500, 800)
	if _, _, err := ProduceCapture(state, producerCapture(12), []PersistedLayout{{ID: 1, Layout: layout}}); err == nil {
		t.Fatal("layout policy mismatch accepted")
	}
	if _, _, err := ProduceCapture(state, producerCapture(12, producerRow(12, 60, 750)), nil); err == nil {
		t.Fatal("missing exact layout accepted")
	}
}
