// Package detour defines the domain rules used to build a temporary route
// around one road cut.
package detour

import (
	"context"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// Criterion selects the lexicographic objective used to choose a detour.
type Criterion string

const (
	CriterionShortestTime    Criterion = "MENOR_TIEMPO"
	CriterionFewestLostStops Criterion = "MENOR_PARADAS_PERDIDAS"
)

// Cut is the single WGS 84 barrier supplied for a detour request.
type Cut struct {
	LineString route.LineString
}

// Input contains the route, its single cut, and the selection criterion.
type Input struct {
	Route     route.Route
	Cut       Cut
	Criterion Criterion
}

// StopAvailability describes whether a variant may retain a stop.
type StopAvailability string

const (
	StopForcedUnavailable StopAvailability = "forced_unavailable"
	StopOptional          StopAvailability = "optional"
	StopRequired          StopAvailability = "required"
)

// ClassifiedStop is one input stop classified by its distance from the cut.
type ClassifiedStop struct {
	Order          int
	ID             string
	DistanceMeters float64
	Availability   StopAvailability
}

// Connection is one directed path in the ordered stop DAG. A connection from
// i to j omits every stop strictly between i and j.
type Connection struct {
	FromStopOrder int
	ToStopOrder   int
	TravelSeconds float64
	EdgeIDs       []int64
	Path          route.LineString
}

// RoutingInput is the domain request passed to the road-network repository.
type RoutingInput struct {
	Route route.Route
	Cut   Cut
	Stops []ClassifiedStop
}

// Selection is the winning path through the ordered stop DAG.
type Selection struct {
	KeptStopOrders    []int
	OmittedStopOrders []int
	TravelSeconds     float64
	Connections       []Connection
}

// Plan contains the stop classification and selected path for one request.
type Plan struct {
	Stops     []ClassifiedStop
	Selection Selection
}

// Repository supplies road-graph coverage and paths between ordered stops.
type Repository interface {
	CutIntersectsGraph(ctx context.Context, cut Cut) (bool, error)
	FindConnections(ctx context.Context, input RoutingInput) ([]Connection, error)
}
