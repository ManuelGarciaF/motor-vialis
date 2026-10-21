package bottlenecks

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProducerHistoricalReplayAndBadMetadataDoNotAdvance(t *testing.T) {
	state := producerState(t)
	state, _ = applyProducer(t, state, producerCapture(11, producerRow(11, 0, 250)))
	state, _ = applyProducer(t, state, producerCapture(12))
	state = restoreProducer(t, state)
	layouts := []PersistedLayout{{ID: 1, Layout: testLayout(t, 1500, 500)}}
	for _, tt := range []struct {
		name   string
		input  ProducerCapture
		replay bool
	}{
		{"historical", producerCapture(11), true},
		{"latest", producerCapture(12), true},
		{"zero ID", producerCapture(0), false},
		{"negative ID", producerCapture(-1), false},
		{"new old receipt", ProducerCapture{ID: 13, ReceivedAt: epoch, Successful: true}, false},
		{"new zero receipt", ProducerCapture{ID: 13, Successful: true}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := cloneProducerState(state)
			next, result, err := ProduceCapture(state, tt.input, layouts)
			if err == nil || errors.Is(err, ErrCaptureAlreadyProcessed) != tt.replay || !reflect.DeepEqual(next, before) || !reflect.DeepEqual(state, before) || !reflect.DeepEqual(result, ProducerResult{}) {
				t.Fatalf("invalid replay/metadata advanced: %v %+v", err, result)
			}
		})
	}
}

func TestProducerCanonicalDuplicatesAndProjectionOrder(t *testing.T) {
	state := producerState(t)
	first := producerRow(11, 0, 500, 0)
	otherOrder := producerRow(11, 0, 0, 500)
	state, result := applyProducer(t, state, producerCapture(11, first, otherOrder))
	if result.Duplicates != 1 || state.Open.Observations != 1 {
		t.Fatalf("canonical duplicate counted twice: %+v %+v", state, result)
	}
	last := producerRow(12, 60, 250)
	state, _ = applyProducer(t, state, producerCapture(12, last))
	_, result = closeProducer(t, state, []ProducerObservation{otherOrder, last})
	if result.Segments[0].TemporallyResolved != 1 {
		t.Fatal("projection ordering changed replay membership or matching")
	}

	bad := producerRow(11, 0, 750)
	layouts := []PersistedLayout{{ID: 1, Layout: testLayout(t, 1500, 500)}}
	initial := producerState(t)
	next, empty, err := ProduceCapture(initial, producerCapture(11, first, bad), layouts)
	if err == nil || !reflect.DeepEqual(next, initial) || !reflect.DeepEqual(empty, ProducerResult{}) {
		t.Fatal("conflicting canonical timestamp accepted")
	}
}

func TestProducerEmptyAndFailedCapturesDoNotInventSequences(t *testing.T) {
	state := producerState(t)
	for id := int64(11); id < 18; id++ {
		input := producerCapture(id)
		if id%2 == 0 {
			input.Successful = false
		}
		var result ProducerResult
		state, result = applyProducer(t, state, input)
		state = restoreProducer(t, state)
		if state.Open != nil || state.MissingCaptures != 0 || result.Closed || len(result.Segments) != 0 {
			t.Fatal("absence inferred a vehicle sequence")
		}
	}
	failed := producerCapture(18, producerRow(18, 0, 250))
	failed.Successful = false
	if _, _, err := ProduceCapture(state, failed, []PersistedLayout{{ID: 1, Layout: testLayout(t, 1500, 500)}}); err == nil {
		t.Fatal("failed capture supplied evidence")
	}
}

func TestProducerStationaryNewTimestampResetsAbsence(t *testing.T) {
	state := producerState(t)
	first := producerRow(11, 0, 750)
	state, _ = applyProducer(t, state, producerCapture(11, first))
	for id := int64(12); id <= 14; id++ {
		state, _ = applyProducer(t, state, producerCapture(id))
	}
	stationary := producerRow(15, 60, 750)
	state, result := applyProducer(t, state, producerCapture(15, stationary))
	if result.Closed || state.MissingCaptures != 0 || state.Open.Observations != 2 {
		t.Fatal("same progress with newer timestamp did not reset absence")
	}
}

func TestProducerLateCandidateCanInvalidatePreviouslyResolvedAlternative(t *testing.T) {
	layout := testLayout(t, 1500, 500)
	anchor, ambiguous := producerRow(11, 0, 250), producerRow(12, 60, 750, 100)
	prefix, err := MatchConservatively(layout, []CandidateObservation{anchor.Candidate, ambiguous.Candidate}, testPolicy())
	if err != nil || prefix.TemporallyResolved != 1 || prefix.Observations[1].Match != Matched {
		t.Fatalf("test prefix should look resolved: %+v %v", prefix, err)
	}
	state := producerState(t)
	state, _ = applyProducer(t, state, producerCapture(11, anchor))
	state, _ = applyProducer(t, state, producerCapture(12, ambiguous))
	contradiction := producerRow(13, 120, 700)
	state, _ = applyProducer(t, state, producerCapture(13, contradiction))
	_, result := closeProducer(t, state, []ProducerObservation{anchor, ambiguous, contradiction})
	if result.Segments[0].Unmatched != 1 || result.Segments[0].TemporallyResolved != 0 {
		t.Fatalf("provisional filtered candidate was frozen: %+v", result)
	}
}

func TestProducerStateStoresDescriptorsNotGPSHistory(t *testing.T) {
	state := producerState(t)
	for id := int64(11); id <= 30; id++ {
		state, _ = applyProducer(t, state, producerCapture(id, producerRow(id, int(id)*60, 250)))
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Candidates", "Candidate", "ProgressMeters", "DistanceMeters", "Latitude", "Longitude"} {
		if strings.Contains(string(data), `"`+field+`"`) {
			t.Fatalf("state archived observation field %s", field)
		}
	}
	if state.Open.Observations != 20 || len(state.Open.Layouts) != 1 || state.Open.LastCaptureID != 30 || state.Open.FirstCaptureID != 11 || state.Open.EarliestAt != epoch.Add(11*time.Minute) {
		t.Fatalf("invalid restart bounds: %+v", state.Open)
	}
}
