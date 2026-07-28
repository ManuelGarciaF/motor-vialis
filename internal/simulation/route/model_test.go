package route

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLineStringUsesGeoJSONCoordinateOrder(t *testing.T) {
	line := LineString{Positions: []Position{
		{Latitude: -34.60, Longitude: -58.38},
		{Latitude: -34.61, Longitude: -58.39},
	}}

	encoded, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(encoded) != `{"type":"LineString","coordinates":[[-58.38,-34.6],[-58.39,-34.61]]}` {
		t.Fatalf("Marshal() = %s", encoded)
	}

	var decoded LineString
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(decoded.Positions) != 2 ||
		decoded.Positions[0].Latitude != -34.60 ||
		decoded.Positions[0].Longitude != -58.38 {
		t.Fatalf("Unmarshal() = %#v", decoded)
	}
}

func TestLineStringRejectsInvalidGeoJSON(t *testing.T) {
	tests := []string{
		`{"type":"Point","coordinates":[-58.38,-34.6]}`,
		`{"type":"LineString","coordinates":[[-58.38]]}`,
	}
	for _, input := range tests {
		var line LineString
		if err := json.Unmarshal([]byte(input), &line); err == nil {
			t.Fatalf("Unmarshal(%s) error = nil", input)
		}
	}
}

func TestRouteJSONOmitsLastPath(t *testing.T) {
	input := `{
		"stops": [
			{
				"id": "A",
				"position": {"latitude": -34.6, "longitude": -58.38},
				"pathToNext": {
					"type": "LineString",
					"coordinates": [[-58.38, -34.6], [-58.39, -34.61]]
				}
			},
			{
				"id": "B",
				"position": {"latitude": -34.61, "longitude": -58.39}
			}
		]
	}`

	var decoded Route
	if err := json.NewDecoder(strings.NewReader(input)).Decode(&decoded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.Stops[0].PathToNext == nil {
		t.Fatal("first path = nil")
	}
	if decoded.Stops[1].PathToNext != nil {
		t.Fatalf("last path = %#v, want nil", decoded.Stops[1].PathToNext)
	}
}
