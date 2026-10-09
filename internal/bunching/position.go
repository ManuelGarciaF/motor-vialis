// Package bunching contains the provider-independent realtime transit model.
package bunching

import "time"

// Position is one structurally valid vehicle observation from a provider.
type Position struct {
	VehicleID            string
	ProviderRouteID      string
	ProviderDirection    int16
	ProviderTripID       string
	RouteShortName       string
	TripHeadsign         string
	AgencyID             int64
	AgencyName           string
	Latitude             float64
	Longitude            float64
	SpeedMetersPerSecond float64
	ObservedAt           time.Time
	Fresh                bool
}

// CaptureStats explains which provider rows were accepted or discarded.
type CaptureStats struct {
	TotalRows     int
	ValidRows     int
	InvalidRows   int
	DuplicateRows int
	StaleRows     int
}

// Capture is one successfully decoded provider response. Positions contains
// structurally valid, deduplicated rows, including stale rows so a later
// persistence layer can retain them without treating them as current state.
type Capture struct {
	// Partial is assigned by the ingestion worker, not inferred by the decoder.
	Partial    bool
	ReceivedAt time.Time
	Positions  []Position
	Stats      CaptureStats
}
