package postgres

import (
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

func TestBottlenecksFinalMeasurementSupportValidation(t *testing.T) {
	version := bottlenecks.ShapeVersion{RouteID: "r", ShapeID: "s", ShapeHash: "hash"}
	layout, err := bottlenecks.NewLayout(version, 1500, 500)
	if err != nil {
		t.Fatal(err)
	}
	policy := persistenceModel().Policy
	entry, exit := passageEpoch, passageEpoch.Add(time.Minute)
	measurement := func() bottlenecks.Measurements {
		p := bottlenecks.Passage{
			VehicleID: "v", Scope: bottlenecks.Scope{ProviderRouteID: "g", Version: version}, Sector: layout.Sectors()[0],
			Entered:  bottlenecks.Crossing{At: entry, BeforeAt: entry, AfterAt: entry},
			Exited:   bottlenecks.Crossing{At: exit, BeforeAt: exit, AfterAt: exit},
			Duration: time.Minute, SpeedKPH: layout.Sectors()[0].LengthMeters() / time.Minute.Seconds() * 3.6,
		}
		return bottlenecks.Measurements{Passages: []bottlenecks.Passage{p}, Rejections: make(map[bottlenecks.Rejection]int)}
	}
	if err = validateMeasurementSupport(layout, "v", policy, exit, measurement()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*bottlenecks.Measurements)
	}{
		{"vehicle", func(m *bottlenecks.Measurements) { m.Passages[0].VehicleID = "other" }},
		{"version", func(m *bottlenecks.Measurements) { m.Passages[0].Scope.Version.ShapeHash = "other" }},
		{"scope", func(m *bottlenecks.Measurements) { m.Passages[0].Scope.ProviderRouteID = "" }},
		{"sector", func(m *bottlenecks.Measurements) { m.Passages[0].Sector.EndMeters++ }},
		{"support", func(m *bottlenecks.Measurements) { m.Passages[0].Entered.AfterAt = entry.Add(-time.Second) }},
		{"beyond closure", func(m *bottlenecks.Measurements) { m.Passages[0].Exited.AfterAt = exit.Add(time.Second) }},
		{"duration", func(m *bottlenecks.Measurements) { m.Passages[0].Duration++ }},
		{"speed", func(m *bottlenecks.Measurements) { m.Passages[0].SpeedKPH++ }},
		{"interior count", func(m *bottlenecks.Measurements) { m.Passages[0].InteriorObservations = -1 }},
		{"gap", func(m *bottlenecks.Measurements) {
			m.Passages[0].MaximumSupportingGap = policy.MaximumGap + time.Second
		}},
		{"support category", func(m *bottlenecks.Measurements) { m.Passages[0].SameGPSInterval = true }},
		{"duplicates", func(m *bottlenecks.Measurements) { m.Duplicates = -1 }},
		{"rejections", func(m *bottlenecks.Measurements) { m.Rejections[bottlenecks.GPSGap] = -1 }},
		{"technical censoring", func(m *bottlenecks.Measurements) {
			p := m.Passages[0]
			m.Incomplete = []bottlenecks.IncompletePassage{{VehicleID: p.VehicleID, Scope: p.Scope, Sector: p.Sector, Entered: p.Entered, Reason: bottlenecks.BatchBoundary}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := measurement()
			test.edit(&m)
			if validateMeasurementSupport(layout, "v", policy, exit, m) == nil {
				t.Fatal("invalid final measurement accepted")
			}
		})
	}
}
