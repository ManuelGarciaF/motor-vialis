package bottlenecks

import "testing"

func TestPersistenceModelRequiresExplicitPolicyAndVersions(t *testing.T) {
	model := PersistenceModel{Name: "experiment-v1", MeasurementVersion: "commercial-v1", MatcherVersion: "conservative-v1", NominalMeters: 500, Policy: testPolicy(), TimeZone: "America/Argentina/Buenos_Aires", RawRetentionDays: 7, ClosureCaptures: 4}
	if err := model.Validate(); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*PersistenceModel)
	}{
		{"name", func(m *PersistenceModel) { m.Name = " " }},
		{"measurement version", func(m *PersistenceModel) { m.MeasurementVersion = "" }},
		{"matcher version", func(m *PersistenceModel) { m.MatcherVersion = "" }},
		{"zero length", func(m *PersistenceModel) { m.NominalMeters = 0 }},
		{"precision", func(m *PersistenceModel) { m.NominalMeters = 0.00000001 }},
		{"gap", func(m *PersistenceModel) { m.Policy.MaximumGap = 0 }},
		{"zone", func(m *PersistenceModel) { m.TimeZone = "UTC" }},
		{"retention", func(m *PersistenceModel) { m.RawRetentionDays = 0 }},
		{"closure", func(m *PersistenceModel) { m.ClosureCaptures = 3 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := model
			tt.edit(&m)
			if m.Validate() == nil {
				t.Fatal("invalid model accepted")
			}
		})
	}
}
