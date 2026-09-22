// Package traffic defines provider-neutral traffic observations.
package traffic

import (
	"fmt"
	"time"

	"github.com/paulmach/orb"
)

// Tile identifies one Web Mercator tile.
type Tile struct {
	Zoom int
	X    int
	Y    int
}

func (tile Tile) String() string {
	return fmt.Sprintf("%d/%d/%d", tile.Zoom, tile.X, tile.Y)
}

// Snapshot contains provider-neutral observations and their provenance.
type Snapshot struct {
	Segments   []Segment
	FetchedAt  time.Time
	TrafficAge time.Duration
	Zoom       int
	TileCount  int
	CacheHits  int
}

// ErrorCode identifies a provider failure without exposing credentials.
type ErrorCode string

const (
	ErrorAPIKeyMissing ErrorCode = "traffic_api_key_missing"
	ErrorUnavailable   ErrorCode = "traffic_unavailable"
	ErrorStale         ErrorCode = "traffic_stale"
	ErrorTileLimit     ErrorCode = "traffic_tile_limit_exceeded"
	ErrorInvalidTile   ErrorCode = "traffic_tile_invalid"
)

// Error is a provider-neutral traffic failure.
type Error struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (err *Error) Error() string {
	if err.Message == "" {
		return string(err.Code)
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Message)
}

func (err *Error) Unwrap() error { return err.Cause }

// Segment is one directed traffic geometry and its current state.
type Segment struct {
	SourceID        string
	Geometry        orb.LineString
	RoadType        string
	RoadCategory    string
	RoadSubcategory string
	RoadCoverage    string
	LeftHandTraffic bool
	SpeedKPH        float64
	HasSpeed        bool
	Closure         bool
}
