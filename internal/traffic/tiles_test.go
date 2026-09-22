package traffic

import "testing"

func TestTilesForGeoJSONUsesPolygonInsteadOfItsWholeBoundingBox(t *testing.T) {
	// A thin diagonal has a large envelope but intersects only a subset of its
	// envelope's tiles.
	area := []byte(`{"type":"Polygon","coordinates":[[[-58.50,-34.70],[-58.49,-34.70],[-58.30,-34.50],[-58.31,-34.50],[-58.50,-34.70]]]}`)
	covered, err := TilesForGeoJSON(area, 14)
	if err != nil {
		t.Fatalf("TilesForGeoJSON() error = %v", err)
	}
	bounds := []byte(`{"type":"Polygon","coordinates":[[[-58.50,-34.70],[-58.30,-34.70],[-58.30,-34.50],[-58.50,-34.50],[-58.50,-34.70]]]}`)
	boundingCover, err := TilesForGeoJSON(bounds, 14)
	if err != nil {
		t.Fatalf("TilesForGeoJSON(bounds) error = %v", err)
	}
	if len(covered) == 0 || len(covered) >= len(boundingCover) {
		t.Fatalf("polygon tiles = %d, bounding tiles = %d", len(covered), len(boundingCover))
	}
	for index := 1; index < len(covered); index++ {
		if covered[index-1].Y > covered[index].Y ||
			(covered[index-1].Y == covered[index].Y && covered[index-1].X >= covered[index].X) {
			t.Fatalf("tiles are not ordered: %v", covered)
		}
	}
}

func TestTilesForGeoJSONRejectsInvalidGeometry(t *testing.T) {
	if _, err := TilesForGeoJSON([]byte(`{"type":"Polygon","coordinates":[]}`), 14); err == nil {
		t.Fatal("TilesForGeoJSON accepted an empty polygon")
	}
}
