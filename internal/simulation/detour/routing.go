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
	// DirectionToleranceDegrees overrides the policy's snapping direction
	// tolerance for this point. Zero keeps the policy value; 180 or more lets
	// the point snap to the nearest edge whatever its direction.
	DirectionToleranceDegrees float64
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
	// GraphMarginMeters agranda el area SOLO para construir el grafo local (no
	// mueve las anclas), para reconectar un ancla que cayo sobre el borde. 0 = sin
	// margen (intento normal). Ver Service.Plan.
	GraphMarginMeters float64
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
	// Default edges are priced with a fallback speed because no traffic data
	// (direct or nearby) covered them. Surfaced so callers can flag the detour
	// as partly estimated instead of treating its time as measured.
	ForwardDefaultEdges int
	ReverseDefaultEdges int
}

// RoutingResult contains every resolvable requested path and graph coverage
// metadata. A missing optional pair is represented by an absent path.
type RoutingResult struct {
	Paths                  []GraphPath
	TopologyAvailablePairs []PointPair
	UnmatchedPointIDs      []int64
	Trace                  GraphTrace
}
