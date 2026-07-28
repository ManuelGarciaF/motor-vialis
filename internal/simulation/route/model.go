// Package route defines the ordered route simulated by the motor.
package route

import (
	"encoding/json"
	"fmt"
)

// Position is a geographic coordinate in WGS 84.
type Position struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// LineString is one ordered path in WGS 84.
//
// Its JSON representation follows GeoJSON, whose coordinate order is
// longitude followed by latitude.
type LineString struct {
	Positions []Position
}

// MarshalJSON encodes a LineString as GeoJSON.
func (line LineString) MarshalJSON() ([]byte, error) {
	coordinates := make([][2]float64, len(line.Positions))
	for index, position := range line.Positions {
		coordinates[index] = [2]float64{
			position.Longitude,
			position.Latitude,
		}
	}
	return json.Marshal(struct {
		Type        string       `json:"type"`
		Coordinates [][2]float64 `json:"coordinates"`
	}{
		Type:        "LineString",
		Coordinates: coordinates,
	})
}

// UnmarshalJSON decodes a GeoJSON LineString.
func (line *LineString) UnmarshalJSON(data []byte) error {
	var value struct {
		Type        string            `json:"type"`
		Coordinates []json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode LineString: %w", err)
	}
	if value.Type != "LineString" {
		return fmt.Errorf("geometry type must be %q: %q", "LineString", value.Type)
	}

	positions := make([]Position, len(value.Coordinates))
	for index, rawCoordinate := range value.Coordinates {
		var coordinate []float64
		if err := json.Unmarshal(rawCoordinate, &coordinate); err != nil {
			return fmt.Errorf("decode coordinate %d: %w", index, err)
		}
		if len(coordinate) != 2 {
			return fmt.Errorf(
				"coordinate %d must contain longitude and latitude",
				index,
			)
		}
		positions[index] = Position{
			Longitude: coordinate[0],
			Latitude:  coordinate[1],
		}
	}
	line.Positions = positions
	return nil
}

// Stop is one occurrence of a stop in an ordered route.
type Stop struct {
	ID         string      `json:"id"`
	Position   Position    `json:"position"`
	PathToNext *LineString `json:"pathToNext,omitempty"`
}

// Route is one independently simulated, ordered route.
type Route struct {
	Stops []Stop `json:"stops"`
}
