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

// Cut is the set of WGS 84 barriers supplied for a detour request. Each entry
// in Lines is one branch of the cut: a simple street cut has a single branch,
// while a T- or H-shaped cut is expressed as several branches. A request that
// supplies a single LineString produces a Cut with one line.
type Cut struct {
	Lines []route.LineString
}

// Input contains the route, its cut, and the selection criterion.
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

// Selection is the winning path through the ordered stop DAG.
type Selection struct {
	KeptStopOrders    []int
	OmittedStopOrders []int
	TravelSeconds     float64
	Connections       []Connection
}

// Repository analyzes a cut against the active graph and calculates local
// paths using current traffic costs.
type Repository interface {
	Analyze(context.Context, route.Route, Cut, Policy) (Analysis, error)
	Route(context.Context, RoutingRequest, Policy) (RoutingResult, error)
}
