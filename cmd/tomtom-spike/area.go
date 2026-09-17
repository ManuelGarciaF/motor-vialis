package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

type geoJSONDocument struct {
	Type       string            `json:"type"`
	Geometry   json.RawMessage   `json:"geometry"`
	Properties map[string]any    `json:"properties"`
	Features   []geoJSONDocument `json:"features"`
}

func loadAreaGeometry(path, featureID string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read area GeoJSON: %w", err)
	}
	var document geoJSONDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode area GeoJSON: %w", err)
	}

	var geometry json.RawMessage
	switch document.Type {
	case "FeatureCollection":
		if featureID == "" {
			return nil, fmt.Errorf("-area-id is required for a FeatureCollection")
		}
		for _, feature := range document.Features {
			if fmt.Sprint(feature.Properties["id"]) == featureID {
				geometry = feature.Geometry
				break
			}
		}
		if len(geometry) == 0 {
			return nil, fmt.Errorf("feature id %q not found in area GeoJSON", featureID)
		}
	case "Feature":
		geometry = document.Geometry
	case "Polygon", "MultiPolygon":
		geometry = data
	default:
		return nil, fmt.Errorf("area GeoJSON type %q is not Polygon or MultiPolygon", document.Type)
	}

	geometry = bytes.TrimSpace(geometry)
	if len(geometry) == 0 || bytes.Equal(geometry, []byte("null")) {
		return nil, fmt.Errorf("selected area has no geometry")
	}
	var geometryType struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(geometry, &geometryType); err != nil {
		return nil, fmt.Errorf("decode selected area geometry: %w", err)
	}
	if geometryType.Type != "Polygon" && geometryType.Type != "MultiPolygon" {
		return nil, fmt.Errorf("selected area geometry is %q, want Polygon or MultiPolygon", geometryType.Type)
	}
	return geometry, nil
}
