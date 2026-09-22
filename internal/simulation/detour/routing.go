package detour

import (
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/traffic"
)

// PointRole determines the error reported when a point cannot be projected
// onto the traffic-covered graph.
type PointRole string

const (
	PointAnchor       PointRole = "anchor"
	PointRequiredStop PointRole = "required_stop"
	PointOptionalStop PointRole = "optional_stop"
)

// RoutingPoint is an anchor or stop projected onto a directed graph edge.
type RoutingPoint struct {
	ID                    int64
	Position              route.Position
	DirectionDegrees      float64
	MaximumDistanceMeters float64
	Role                  PointRole
}

// PointPair requests the fastest path between two routing points.
type PointPair struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// RoutingRequest contains one local graph, its traffic observations, and the
// ordered point pairs whose paths are needed by the selector.
type RoutingRequest struct {
	Analysis Analysis
	Traffic  []traffic.Segment
	Points   []RoutingPoint
	Pairs    []PointPair
}

// GraphPath is the fastest traffic-priced path for one point pair.
type GraphPath struct {
	From          int64
	To            int64
	TravelSeconds float64
	EdgeIDs       []int64
	Geometry      route.LineString
}

// GraphTrace reports how traffic coverage shaped the local routing graph.
type GraphTrace struct {
	CandidateEdges        int
	BlockedEdges          int
	ForwardDirectEdges    int
	ReverseDirectEdges    int
	ForwardEstimatedEdges int
	ReverseEstimatedEdges int
}

// RoutingResult contains every resolvable requested path and graph coverage
// metadata. A missing optional pair is represented by an absent path.
type RoutingResult struct {
	Paths                  []GraphPath
	TopologyAvailablePairs []PointPair
	UnmatchedPointIDs      []int64
	Trace                  GraphTrace
}
