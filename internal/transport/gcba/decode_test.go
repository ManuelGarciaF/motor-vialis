package gcba

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestDecodeRejectsRowsIndividually(t *testing.T) {
	body, err := os.ReadFile("testdata/vehicle-positions-normal.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name         string
		change       func(map[string]any)
		valid, stale int
	}{
		{"fresh", func(r map[string]any) {}, 1, 0},
		{"age-boundary", func(r map[string]any) { r["timestamp"] = testNow.Add(-5 * time.Minute).Unix() }, 1, 0},
		{"stale", func(r map[string]any) { r["timestamp"] = testNow.Add(-5*time.Minute - time.Second).Unix() }, 1, 1},
		{"future-boundary", func(r map[string]any) { r["timestamp"] = testNow.Add(time.Minute).Unix() }, 1, 0},
		{"future", func(r map[string]any) { r["timestamp"] = testNow.Add(time.Minute + time.Second).Unix() }, 0, 0},
		{"missing-latitude", func(r map[string]any) { delete(r, "latitude") }, 0, 0},
		{"null-longitude", func(r map[string]any) { r["longitude"] = nil }, 0, 0},
		{"missing-direction", func(r map[string]any) { delete(r, "direction") }, 0, 0},
		{"fractional-direction", func(r map[string]any) { r["direction"] = 0.5 }, 0, 0},
		{"negative-speed", func(r map[string]any) { r["speed"] = -1 }, 0, 0},
		{"fractional-time", func(r map[string]any) { r["timestamp"] = 1735689600.5 }, 0, 0},
		{"empty-id", func(r map[string]any) { r["id"] = " " }, 0, 0},
		{"nonessential-metadata", func(r map[string]any) { delete(r, "route_short_name"); delete(r, "trip_headsign") }, 1, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			row := make(map[string]any)
			for k, v := range rows[0] {
				row[k] = v
			}

			test.change(row)
			encoded, _ := json.Marshal([]any{row, rows[1]})
			capture, err := decode(encoded, testNow, 5*time.Minute, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if capture.Stats.ValidRows != test.valid+1 || capture.Stats.StaleRows != test.stale || capture.Stats.InvalidRows != 1-test.valid {
				t.Fatalf("stats: %+v", capture.Stats)
			}
		})
	}
}

func TestDecodeDeduplicatesByVehicleAndTimestamp(t *testing.T) {
	body, _ := os.ReadFile("testdata/vehicle-positions-normal.json")
	var rows []map[string]any
	json.Unmarshal(body, &rows)
	duplicate := make(map[string]any)
	for k, v := range rows[0] {
		duplicate[k] = v
	}

	duplicate["route_id"] = "different-route"
	encoded, _ := json.Marshal([]any{rows[0], duplicate, rows[1]})
	capture, err := decode(encoded, testNow, 5*time.Minute, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if capture.Stats.DuplicateRows != 1 || len(capture.Positions) != 2 || capture.Positions[0].ProviderRouteID != "route-1" {
		t.Fatalf("capture: %+v", capture)
	}
}
