package tomtom

import (
	"errors"
	"os"
	"testing"
)

func TestDecodeAnonymizedTrafficFlowTile(t *testing.T) {
	data, err := os.ReadFile("testdata/traffic-flow-anonymized.pbf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	segments, err := Decode(data, Tile{Zoom: 14, X: 8192, Y: 8192}, Limits{
		MaximumTileBytes: 1 << 20,
		MaximumFeatures:  10,
	})
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	if len(segments) != 4 {
		t.Fatalf("segments = %d, want 4", len(segments))
	}
	first := segments[0]
	if first.RoadCategory != "primary" || first.SpeedKPH != 35 ||
		!first.HasSpeed || first.Closure || first.RoadCoverage != "full" {
		t.Fatalf("first segment = %#v", first)
	}
	if segments[1].RoadCoverage != "one_side" || !segments[1].Closure {
		t.Fatalf("one-side closure was not preserved: %#v", segments[1])
	}
	last := segments[len(segments)-1]
	if last.HasSpeed || last.SpeedKPH != 0 || last.RoadSubcategory != "" {
		t.Fatalf("missing properties were not preserved as absent: %#v", last)
	}
	for _, segment := range segments {
		if len(segment.Geometry) < 2 {
			t.Fatalf("segment %s has invalid geometry", segment.SourceID)
		}
	}
}

func TestDecodeEnforcesLimits(t *testing.T) {
	data, err := os.ReadFile("testdata/traffic-flow-anonymized.pbf")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tests := []Limits{
		{MaximumTileBytes: len(data) - 1, MaximumFeatures: 10},
		{MaximumTileBytes: len(data), MaximumFeatures: 2},
	}
	for _, limits := range tests {
		_, err := Decode(data, Tile{Zoom: 14, X: 8192, Y: 8192}, limits)
		var providerError *Error
		if !errors.As(err, &providerError) || providerError.Code != ErrorInvalidTile {
			t.Fatalf("Decode error = %v, want traffic_tile_invalid", err)
		}
	}
}

func TestDecodeRejectsMalformedTile(t *testing.T) {
	_, err := Decode([]byte("not a vector tile"), Tile{Zoom: 14, X: 1, Y: 1}, Limits{
		MaximumTileBytes: 1024,
		MaximumFeatures:  10,
	})
	var providerError *Error
	if !errors.As(err, &providerError) || providerError.Code != ErrorInvalidTile {
		t.Fatalf("Decode error = %v, want traffic_tile_invalid", err)
	}
}
