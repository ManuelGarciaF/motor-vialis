// Package traveltime estimates commercial travel time from nearby GTFS route
// segments.
package traveltime

import (
	"context"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// Confidence describes the quality of the references used by an estimate.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// ReferenceSource describes where an estimate obtained its commercial pace.
type ReferenceSource string

const (
	SourceLocal100 ReferenceSource = "local_100m"
	SourceLocal300 ReferenceSource = "local_300m"
	SourceLocal800 ReferenceSource = "local_800m"
	SourceGlobal   ReferenceSource = "global"
)

// Paces stores seconds per meter for each statistical GTFS scenario.
type Paces struct {
	OffPeak float64
	Typical float64
	Peak    float64
}

// Segment is one input path between consecutive stops.
type Segment struct {
	Order             int
	OriginStopID      string
	DestinationStopID string
	Path              route.LineString
}

// Reference is one existing GTFS route segment close to an input segment.
type Reference struct {
	RouteID        int64
	RadiusMeters   float64
	DistanceMeters float64
	OverlapMeters  float64
	Paces          Paces
}

// MeasuredSegment contains a PostGIS measurement and its possible references.
type MeasuredSegment struct {
	Segment
	LengthMeters float64
	References   []Reference
}

// GlobalPaces is the final fallback calculated from all valid GTFS routes.
type GlobalPaces struct {
	Paces      Paces
	RouteCount int
}

// Repository measures input geometry and finds local and global GTFS paces.
type Repository interface {
	FindSegmentReferences(
		ctx context.Context,
		segments []Segment,
		policy Policy,
	) ([]MeasuredSegment, error)
	FindGlobalPaces(
		ctx context.Context,
		policy Policy,
	) (GlobalPaces, error)
}

// SegmentResult is the travel-time estimate for one pair of stops.
type SegmentResult struct {
	OriginStopID        string          `json:"originStopId"`
	DestinationStopID   string          `json:"destinationStopId"`
	DistanceMeters      float64         `json:"distanceMeters"`
	OffPeakSeconds      int64           `json:"offPeakSeconds"`
	TypicalSeconds      int64           `json:"typicalSeconds"`
	PeakSeconds         int64           `json:"peakSeconds"`
	Confidence          Confidence      `json:"confidence"`
	ReferenceRouteCount int             `json:"referenceRouteCount"`
	Source              ReferenceSource `json:"source"`
}

// Result contains route totals and the estimates used to construct them.
type Result struct {
	TotalDistanceMeters float64         `json:"-"`
	OffPeakSeconds      int64           `json:"offPeakSeconds"`
	TypicalSeconds      int64           `json:"typicalSeconds"`
	PeakSeconds         int64           `json:"peakSeconds"`
	Confidence          Confidence      `json:"confidence"`
	BySegment           []SegmentResult `json:"bySegment"`
}
