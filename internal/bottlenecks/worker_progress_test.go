package bottlenecks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProducerWorkerLogsOnlyCommittedQualityCounters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	calls := 0
	worker := producerWorkerForTest(t, func(context.Context, string) (ProducerProgress, error) {
		calls++
		p := ProducerProgress{
			Processed: true, Checkpoint: int64(calls), Vehicles: 3, ClosedVehicles: 1,
			Passages: 2, Incomplete: 1, LateObservations: 4, Duplicates: 5,
			SpatiallyUnique: 6, TemporallyResolved: 7, Ambiguous: 8, Unmatched: 9,
			Rejections: map[Rejection]int{GPSGap: 10},
		}
		if calls == 3 {
			return p, errors.New("uncommitted operation")
		}
		return p, nil
	}, &output)
	worker.options.MaxCapturesPerCycle = 3
	worker.wait = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.Split(output.String(), "\n")[0]), &event); err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]float64{
		"captures": 2, "checkpoint": 2, "vehicle_capture_states": 6, "closed_sequences": 2,
		"passages": 4, "incomplete": 2, "late_observations": 8, "duplicates": 10,
		"spatially_unique": 12, "temporally_resolved": 14, "ambiguous": 16, "unmatched": 18,
	} {
		if event[name] != expected {
			t.Errorf("%s: got %v, want %v", name, event[name], expected)
		}
	}
	if rejections, ok := event["rejections"].(map[string]any); !ok || rejections[string(GPSGap)] != float64(20) {
		t.Fatalf("rejection counters lost: %v", event["rejections"])
	}
}
