package tomtom

import "testing"

func TestTilesForBoundsReturnsDeterministicCoverage(t *testing.T) {
	got, err := TilesForBounds(Bounds{
		MinLatitude:  -34.62,
		MinLongitude: -58.40,
		MaxLatitude:  -34.60,
		MaxLongitude: -58.37,
	}, 14)
	if err != nil {
		t.Fatalf("TilesForBounds returned error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("TilesForBounds returned no tiles")
	}
	for index := 1; index < len(got); index++ {
		previous, current := got[index-1], got[index]
		if previous.Y > current.Y ||
			(previous.Y == current.Y && previous.X >= current.X) {
			t.Fatalf("tiles are not ordered and unique: %v then %v", previous, current)
		}
	}
}

func TestTilesForBoundsCrossesAntimeridian(t *testing.T) {
	got, err := TilesForBounds(Bounds{
		MinLatitude:  -1,
		MinLongitude: 179,
		MaxLatitude:  1,
		MaxLongitude: -179,
	}, 2)
	if err != nil {
		t.Fatalf("TilesForBounds returned error: %v", err)
	}
	seenX := make(map[int]bool)
	for _, tile := range got {
		seenX[tile.X] = true
	}
	if !seenX[0] || !seenX[3] || len(seenX) != 2 {
		t.Fatalf("antimeridian x columns = %v, want only 0 and 3", seenX)
	}
}

func TestTilesForBoundsRejectsInvalidInput(t *testing.T) {
	if _, err := TilesForBounds(Bounds{
		MinLatitude: -90,
		MaxLatitude: 0,
	}, 14); err == nil {
		t.Fatal("TilesForBounds accepted latitude outside Web Mercator")
	}
	if _, err := TilesForBounds(Bounds{}, 23); err == nil {
		t.Fatal("TilesForBounds accepted zoom 23")
	}
}
