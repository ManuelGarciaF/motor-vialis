// Package demand contains the business rules used to estimate potential demand
// for one ordered route.
package demand

// CellID identifies one cell in the spatial demand grid.
type CellID string

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
	OriginStopOrder      int     `json:"originStopOrder"`
	OriginStopID         string  `json:"originStopId"`
	DestinationStopOrder int     `json:"destinationStopOrder"`
	DestinationStopID    string  `json:"destinationStopId"`
	GrossDemand          float64 `json:"grossDemand"`
	PotentialDemand      float64 `json:"potentialDemand"`
}

// Result contains potential demand for one route.
type Result struct {
	GrossDemand     float64          `json:"grossDemand"`
	PotentialDemand float64          `json:"potentialDemand"`
	ByStopPair      []StopPairDemand `json:"byStopPair"`
}
