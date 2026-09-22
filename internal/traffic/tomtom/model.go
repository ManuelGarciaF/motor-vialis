// Package tomtom obtains and decodes TomTom Traffic Flow vector tiles.
package tomtom

import "github.com/ManuelGarciaF/vialis-motor/internal/traffic"

// Provider-neutral aliases keep the TomTom API convenient without coupling the
// detour domain to this provider.
type (
	Tile      = traffic.Tile
	Bounds    = traffic.Bounds
	Segment   = traffic.Segment
	Snapshot  = traffic.Snapshot
	ErrorCode = traffic.ErrorCode
	Error     = traffic.Error
)

const (
	ErrorAPIKeyMissing = traffic.ErrorAPIKeyMissing
	ErrorUnavailable   = traffic.ErrorUnavailable
	ErrorStale         = traffic.ErrorStale
	ErrorTileLimit     = traffic.ErrorTileLimit
	ErrorInvalidTile   = traffic.ErrorInvalidTile
)

// Limits bound decoder work before provider data is accepted.
type Limits struct {
	MaximumTileBytes int
	MaximumFeatures  int
}
