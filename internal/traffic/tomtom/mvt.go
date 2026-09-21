package tomtom

import (
	"fmt"

	"github.com/paulmach/orb"
	"github.com/paulmach/orb/encoding/mvt"
	"github.com/paulmach/orb/maptile"
)

const trafficFlowLayer = "Traffic flow"

// Decode converts a Traffic Flow tile into provider-neutral line segments.
func Decode(data []byte, tile Tile, limits Limits) ([]Segment, error) {
	if len(data) == 0 {
		return nil, invalidTile(tile, "tile is empty", nil)
	}
	if limits.MaximumTileBytes <= 0 || limits.MaximumFeatures <= 0 {
		return nil, fmt.Errorf("decode traffic tile: limits must be positive")
	}
	if len(data) > limits.MaximumTileBytes {
		return nil, invalidTile(tile, fmt.Sprintf(
			"tile exceeds %d bytes",
			limits.MaximumTileBytes,
		), nil)
	}

	layers, err := mvt.Unmarshal(data)
	if err != nil {
		return nil, invalidTile(tile, "cannot decode vector tile", err)
	}
	featureCount := 0
	for _, layer := range layers {
		featureCount += len(layer.Features)
		if featureCount > limits.MaximumFeatures {
			return nil, invalidTile(tile, fmt.Sprintf(
				"tile exceeds %d features",
				limits.MaximumFeatures,
			), nil)
		}
	}

	mapTile := maptile.New(uint32(tile.X), uint32(tile.Y), maptile.Zoom(tile.Zoom))
	var result []Segment
	for _, layer := range layers {
		if layer.Name != trafficFlowLayer {
			continue
		}
		layer.ProjectToWGS84(mapTile)
		for featureIndex, feature := range layer.Features {
			properties := feature.Properties
			speed, hasSpeed := numericValue(properties["traffic_level"])
			base := Segment{
				RoadType:        stringValue(properties["road_type"]),
				RoadCategory:    stringValue(properties["road_category"]),
				RoadSubcategory: stringValue(properties["road_subcategory"]),
				RoadCoverage:    stringValue(properties["traffic_road_coverage"]),
				LeftHandTraffic: boolValue(properties["left_hand_traffic"]),
				SpeedKPH:        speed,
				HasSpeed:        hasSpeed,
				Closure:         boolValue(properties["road_closure"]),
			}
			appendLine := func(line orb.LineString, part int) {
				if len(line) < 2 {
					return
				}
				segment := base
				segment.SourceID = fmt.Sprintf(
					"%s/%d/%d",
					tile.String(),
					featureIndex,
					part,
				)
				segment.Geometry = append(orb.LineString(nil), line...)
				result = append(result, segment)
			}
			switch geometry := feature.Geometry.(type) {
			case orb.LineString:
				appendLine(geometry, 0)
			case orb.MultiLineString:
				for part, line := range geometry {
					appendLine(line, part)
				}
			}
		}
	}
	return result, nil
}

func invalidTile(tile Tile, message string, cause error) error {
	return &Error{
		Code:    ErrorInvalidTile,
		Message: fmt.Sprintf("tile %s: %s", tile, message),
		Cause:   cause,
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func boolValue(value any) bool {
	boolean, _ := value.(bool)
	return boolean
}

func numericValue(value any) (float64, bool) {
	switch value := value.(type) {
	case float64:
		return value, true
	case float32:
		return float64(value), true
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	default:
		return 0, false
	}
}
