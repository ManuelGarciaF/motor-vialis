// Package simulation orchestrates the independent estimators for one route.
package simulation

import (
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// Route input aliases preserve the concise simulation API while their shared
// implementation lives in the route package.
type (
	Position        = route.Position
	LineString      = route.LineString
	Stop            = route.Stop
	Route           = route.Route
	ValidationError = route.ValidationError
)

// Metrics contains the route-level measurements produced by the simulation.
type Metrics struct {
	TotalDistanceMeters float64           `json:"totalDistanceMeters"`
	TravelTime          traveltime.Result `json:"travelTime"`
}

// Result groups independent simulation calculations.
type Result struct {
	Demand  demand.Result `json:"demand"`
	Metrics Metrics       `json:"metrics"`
}
