// Package simulation contains the business rules used to estimate potential
// demand for one ordered route.
package simulation

// CellID identifies one cell in the spatial demand grid.
type CellID string

// Position is a geographic coordinate in WGS 84.
type Position struct {
	Latitude  float64
	Longitude float64
}

// Stop is one occurrence of a stop in an ordered route.
type Stop struct {
	ID       string
	Position Position
}

// Route is one independently simulated, ordered route.
type Route struct {
	Stops []Stop
}

// CellCandidate associates a stop with a nearby demand cell.
type CellCandidate struct {
	StopOrder      int
	StopID         string
	CellID         CellID
	DistanceMeters float64
}

// AssignedCell is a demand cell assigned exclusively to one stop.
type AssignedCell struct {
	StopOrder     int
	StopID        string
	CellID        CellID
	Accessibility float64
}

// StopPairDemand contains demand between two ordered stops.
type StopPairDemand struct {
	OriginStopOrder      int
	OriginStopID         string
	DestinationStopOrder int
	DestinationStopID    string
	GrossDemand          float64
	PotentialDemand      float64
}

// Result contains potential demand for one route.
type Result struct {
	GrossDemand     float64
	PotentialDemand float64
	ByStopPair      []StopPairDemand
}
