package config

import (
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func TestBottlenecksModelSnapshotsConfiguredPolicy(t *testing.T) {
	model := BottlenecksModel()
	if err := model.Validate(); err != nil {
		t.Fatal(err)
	}
	if model.Name == "" || model.MeasurementVersion != BottlenecksMeasurementVersion ||
		model.MatcherVersion != BottlenecksMatcherVersion || model.NominalMeters != BottlenecksSegmentMeters ||
		model.TimeZone != BottlenecksAnalysisTimeZone || model.RawRetentionDays != RealtimeRawRetentionDays ||
		model.ClosureCaptures != bottlenecks.MissingCaptureLimit {
		t.Fatalf("measurement configuration is not sourced from constants: %+v", model)
	}
	policy := BottlenecksMeasurementPolicy()
	if model.Policy != policy || policy.MaximumGap != BottlenecksMaximumGap ||
		policy.MaximumDistanceMeters != BottlenecksMaximumDistanceMeters || policy.MaximumSpeedKPH != BottlenecksMaximumSpeedKPH {
		t.Fatalf("measurement policy drifted: %+v", policy)
	}
	model.Policy.MaximumGap += time.Second
	if BottlenecksModel().Policy != policy {
		t.Fatal("caller's snapshot mutated shared configuration")
	}
}

func TestBottlenecksDetectionPolicy(t *testing.T) {
	policy := BottlenecksDetectionPolicy()
	if policy.SpeedBelowKPH != 6 || policy.MinimumSlowPassages != 2 {
		t.Fatalf("approved experimental rule drifted: %+v", policy)
	}
	policy.MinimumSlowPassages = 100
	if BottlenecksDetectionPolicy().MinimumSlowPassages != BottlenecksMinimumSlowPassages {
		t.Fatal("caller mutated shared classification policy")
	}
}

func TestBottlenecksOperationalLimitsArePositive(t *testing.T) {
	if BottlenecksInterval <= 0 || BottlenecksCycleTimeout <= 0 ||
		BottlenecksPoolMaxConnections <= 0 || BottlenecksMaxCapturesPerCycle <= 0 {
		t.Fatal("invalid bottlenecks scheduling/resource configuration")
	}
}
