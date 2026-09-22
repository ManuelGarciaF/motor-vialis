package traffic

import (
	"fmt"
	"sort"

	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/maptile/tilecover"
)

// TilesForGeoJSON returns the exact tile cover of a GeoJSON geometry.
func TilesForGeoJSON(data []byte, zoom int) ([]Tile, error) {
	if zoom < 0 || zoom > 22 {
		return nil, fmt.Errorf("tile zoom must be between 0 and 22")
	}
	geometry, err := geojson.UnmarshalGeometry(data)
	if err != nil {
		return nil, fmt.Errorf("decode tile coverage geometry: %w", err)
	}
	if geometry == nil || geometry.Geometry() == nil {
		return nil, fmt.Errorf("tile coverage geometry is empty")
	}
	covered, err := tilecover.Geometry(geometry.Geometry(), maptile.Zoom(zoom))
	if err != nil {
		return nil, fmt.Errorf("calculate tile coverage: %w", err)
	}
	if len(covered) == 0 {
		return nil, fmt.Errorf("tile coverage geometry is empty")
	}
	result := make([]Tile, 0, len(covered))
	for tile := range covered {
		result = append(result, Tile{Zoom: int(tile.Z), X: int(tile.X), Y: int(tile.Y)})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Y != result[right].Y {
			return result[left].Y < result[right].Y
		}
		return result[left].X < result[right].X
	})
	return result, nil
}
