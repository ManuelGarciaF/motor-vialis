// Package route defines the ordered route simulated by the motor.
package route

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Jurisdiction identifies the tariff authority selected for a simulation.
// It is explicit input because regulatory jurisdiction cannot be inferred
// reliably from a proposed route geometry alone.
type Jurisdiction string

const (
	JurisdictionCABA     Jurisdiction = "caba"
	JurisdictionProvince Jurisdiction = "province"
	JurisdictionNational Jurisdiction = "national"
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
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

// MultiLineString is one or more ordered paths in WGS 84.
//
// It decodes from either a GeoJSON LineString (treated as a single branch) or a
// GeoJSON MultiLineString (several branches), and always encodes as a GeoJSON
// MultiLineString. Like LineString, its coordinate order is longitude followed
// by latitude.
type MultiLineString struct {
	Lines []LineString
}

// MarshalJSON encodes a MultiLineString as GeoJSON.
func (multi MultiLineString) MarshalJSON() ([]byte, error) {
	coordinates := make([][][2]float64, len(multi.Lines))
	for lineIndex, line := range multi.Lines {
		lineCoordinates := make([][2]float64, len(line.Positions))
		for index, position := range line.Positions {
			lineCoordinates[index] = [2]float64{
				position.Longitude,
				position.Latitude,
			}
		}
		coordinates[lineIndex] = lineCoordinates
	}
	return json.Marshal(struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}{
		Type:        "MultiLineString",
		Coordinates: coordinates,
	})
}

// UnmarshalJSON decodes either a GeoJSON LineString (one branch) or a GeoJSON
// MultiLineString (several branches). A plain LineString keeps working exactly
// as before, so existing clients that send a single cut are unaffected.
func (multi *MultiLineString) UnmarshalJSON(data []byte) error {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return fmt.Errorf("decode geometry type: %w", err)
	}
	switch head.Type {
	case "LineString":
		var line LineString
		if err := line.UnmarshalJSON(data); err != nil {
			return err
		}
		multi.Lines = []LineString{line}
		return nil
	case "MultiLineString":
		var value struct {
			Type        string              `json:"type"`
			Coordinates [][]json.RawMessage `json:"coordinates"`
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("decode MultiLineString: %w", err)
		}
		lines := make([]LineString, len(value.Coordinates))
		for lineIndex, rawLine := range value.Coordinates {
			positions := make([]Position, len(rawLine))
			for index, rawCoordinate := range rawLine {
				var coordinate []float64
				if err := json.Unmarshal(rawCoordinate, &coordinate); err != nil {
					return fmt.Errorf(
						"decode line %d coordinate %d: %w",
						lineIndex, index, err,
					)
				}
				if len(coordinate) != 2 {
					return fmt.Errorf(
						"line %d coordinate %d must contain longitude and latitude",
						lineIndex, index,
					)
				}
				positions[index] = Position{
					Longitude: coordinate[0],
					Latitude:  coordinate[1],
				}
			}
			lines[lineIndex] = LineString{Positions: positions}
		}
		multi.Lines = lines
		return nil
	default:
		return fmt.Errorf(
			"geometry type must be %q or %q: %q",
			"LineString", "MultiLineString", head.Type,
		)
	}
}

// Stop is one occurrence of a stop in an ordered route.
type Stop struct {
	ID         string      `json:"id"`
	Position   Position    `json:"position"`
	PathToNext *LineString `json:"pathToNext,omitempty"`
}

// Route is one independently simulated, ordered route.
type Route struct {
	// Jurisdiction is omitted when empty so a route exported from stored GTFS
	// data — which records no tariff authority — does not claim one. Validate
	// still requires it before anything is simulated.
	Jurisdiction Jurisdiction `json:"jurisdiction,omitempty"`
	Stops        []Stop       `json:"stops"`
}
