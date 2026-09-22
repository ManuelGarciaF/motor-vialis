package detour

import (
	"encoding/json"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// Bounds is the WGS 84 envelope of the local search area.
type Bounds struct {
	MinLatitude  float64
	MinLongitude float64
	MaxLatitude  float64
	MaxLongitude float64
}

// Anchor is a point where the original route enters or leaves the local search
// area. Fraction locates it on the corresponding PathToNext.
type Anchor struct {
	SegmentOrder     int
	Fraction         float64
	Position         route.Position
	DirectionDegrees float64
}

// AffectedInterval is one contiguous portion of the original route inside the
// local search area.
type AffectedInterval struct {
	Order int
	Entry Anchor
	Exit  Anchor
}

// Analysis describes how one cut intersects the active graph and input route.
type Analysis struct {
	GraphLoadID      int64
	SearchArea       json.RawMessage
	ForbiddenArea    json.RawMessage
	SearchBounds     Bounds
	BlockedStreetIDs []int64
	RouteAffected    bool
	Intervals        []AffectedInterval
}
