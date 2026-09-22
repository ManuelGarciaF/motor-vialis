package traffic

import (
	"fmt"
	"math"
	"sort"

	"github.com/paulmach/orb/geojson"
	"github.com/paulmach/orb/maptile"
	"github.com/paulmach/orb/maptile/tilecover"
)

const maximumMercatorLatitude = 85.05112878

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

// TilesForBounds returns every Web Mercator tile intersecting the bounding box,
// ordered by row and then column without duplicates.
func TilesForBounds(bounds Bounds, zoom int) ([]Tile, error) {
	if zoom < 0 || zoom > 22 {
		return nil, fmt.Errorf("tile zoom must be between 0 and 22")
	}
	if !finite(bounds.MinLatitude) || !finite(bounds.MaxLatitude) ||
		bounds.MinLatitude < -maximumMercatorLatitude ||
		bounds.MaxLatitude > maximumMercatorLatitude ||
		bounds.MinLatitude > bounds.MaxLatitude {
		return nil, fmt.Errorf("invalid Web Mercator latitude bounds")
	}
	if !finite(bounds.MinLongitude) || !finite(bounds.MaxLongitude) ||
		bounds.MinLongitude < -180 || bounds.MinLongitude > 180 ||
		bounds.MaxLongitude < -180 || bounds.MaxLongitude > 180 {
		return nil, fmt.Errorf("invalid longitude bounds")
	}

	minimumY := latitudeToTileY(bounds.MaxLatitude, zoom)
	maximumY := latitudeToTileY(bounds.MinLatitude, zoom)
	xRanges := longitudeTileRanges(bounds.MinLongitude, bounds.MaxLongitude, zoom)
	seen := make(map[Tile]struct{})
	for y := minimumY; y <= maximumY; y++ {
		for _, xRange := range xRanges {
			for x := xRange[0]; x <= xRange[1]; x++ {
				seen[Tile{Zoom: zoom, X: x, Y: y}] = struct{}{}
			}
		}
	}
	result := make([]Tile, 0, len(seen))
	for tile := range seen {
		result = append(result, tile)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Y != result[right].Y {
			return result[left].Y < result[right].Y
		}
		return result[left].X < result[right].X
	})
	return result, nil
}

func longitudeTileRanges(minimum, maximum float64, zoom int) [][2]int {
	lastX := (1 << zoom) - 1
	minimumX := longitudeToTileX(minimum, zoom)
	maximumX := longitudeToTileX(maximum, zoom)
	if minimum <= maximum {
		return [][2]int{{minimumX, maximumX}}
	}
	return [][2]int{{minimumX, lastX}, {0, maximumX}}
}

func longitudeToTileX(longitude float64, zoom int) int {
	size := math.Exp2(float64(zoom))
	x := int(math.Floor((longitude + 180) / 360 * size))
	return max(0, min(int(size)-1, x))
}

func latitudeToTileY(latitude float64, zoom int) int {
	latitudeRadians := latitude * math.Pi / 180
	size := math.Exp2(float64(zoom))
	y := int(math.Floor(
		(1 - math.Asinh(math.Tan(latitudeRadians))/math.Pi) / 2 * size,
	))
	return max(0, min(int(size)-1, y))
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
