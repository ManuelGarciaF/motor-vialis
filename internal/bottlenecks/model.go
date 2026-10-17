// Package bottlenecks measures commercial bus passages for H0012.
// It does not attribute road causes or classify recurrent bottlenecks.
package bottlenecks

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// ShapeVersion identifies a directed geometry independently of regenerable DB IDs.
type ShapeVersion struct {
	RouteID   string
	Direction int16
	ShapeID   string
	ShapeHash string
}

func (v ShapeVersion) validate() error {
	if strings.TrimSpace(v.RouteID) == "" ||
		strings.TrimSpace(v.ShapeID) == "" ||
		strings.TrimSpace(v.ShapeHash) == "" ||
		v.Direction < 0 ||
		v.Direction > 1 {
		return fmt.Errorf("invalid shape version")
	}
	return nil
}

// Scope prevents continuity between provider branches sharing a GTFS geometry.
type Scope struct {
	ProviderRouteID   string
	ProviderDirection int16
	Version           ShapeVersion
}

func (s Scope) validate() error {
	if strings.TrimSpace(s.ProviderRouteID) == "" || s.ProviderDirection < 0 || s.ProviderDirection > 1 {
		return fmt.Errorf("invalid provider scope")
	}
	return s.Version.validate()
}

type MatchStatus string

const (
	Unmatched MatchStatus = ""
	Matched   MatchStatus = "matched"
	Ambiguous MatchStatus = "ambiguous"
)

// Observation must come from a trajectory matcher. ProgressMeters is distance
// along the versioned geometry, not displacement in a straight line. All
// distances (including layout length) must use the same measurement method.
// Ambiguous/unmatched points break continuity; nearest-point projection alone
// is not sufficient to label a point Matched on a loop or crossing.
type Observation struct {
	VehicleID             string
	Scope                 Scope
	ObservedAt            time.Time
	Match                 MatchStatus
	ProgressMeters        float64
	DistanceToShapeMeters float64
}

// MeasurementPolicy contains explicit quality limits, not detection thresholds.
// There are deliberately no production defaults pending calibration.
type MeasurementPolicy struct {
	MaximumGap            time.Duration
	MaximumDistanceMeters float64
	MaximumSpeedKPH       float64
}

func (p MeasurementPolicy) validate() error {
	if p.MaximumGap <= 0 || !finite(p.MaximumDistanceMeters) || p.MaximumDistanceMeters < 0 || !finite(p.MaximumSpeedKPH) || p.MaximumSpeedKPH <= 0 {
		return fmt.Errorf("invalid measurement policy")
	}
	return nil
}

// Crossing records both the estimated instant and the GPS interval supporting it.
// An exact observation on a boundary has BeforeAt == AfterAt == At.
type Crossing struct {
	At, BeforeAt, AfterAt time.Time
}

// Passage includes both cases: with and without interior GPS observations.
// Neither case uses the provider's instantaneous speed. InteriorObservations
// counts unique timestamps strictly inside the sector, not on its boundaries.
type Passage struct {
	VehicleID            string
	Scope                Scope
	Sector               Sector
	Entered, Exited      Crossing
	Duration             time.Duration
	SpeedKPH             float64
	InteriorObservations int
	MaximumSupportingGap time.Duration
	SameGPSInterval      bool
}

type Rejection string

const (
	UnresolvedMatch  Rejection = "unresolved_match"
	WrongVersion     Rejection = "wrong_version"
	OutsideShape     Rejection = "outside_shape"
	OutsideProgress  Rejection = "outside_progress"
	ScopeChanged     Rejection = "scope_changed"
	GPSGap           Rejection = "gps_gap"
	Backwards        Rejection = "backwards"
	ImplausibleSpeed Rejection = "implausible_speed"
	// BatchBoundary is censoring, not evidence of a bad GPS or failed service.
	BatchBoundary      Rejection = "batch_boundary"
	DegenerateCrossing Rejection = "degenerate_crossing"
)

// IncompletePassage has a supported entry but no admissible exit. It is not
// an inferred actual trip lost: entries before the batch are unknowable.
// Vehicle IDs remain internal and are only counted in coverage reports.
type IncompletePassage struct {
	VehicleID string
	Scope     Scope
	Sector    Sector
	Entered   Crossing
	Reason    Rejection
}

// Measurements is an in-memory batch result, not an API response. Rejections
// use one reason per discarded observation or broken edge, in that priority.
// Exact duplicates are ignored. Conflicting vehicle/timestamp rows are errors.
type Measurements struct {
	Passages   []Passage
	Incomplete []IncompletePassage
	Duplicates int
	Rejections map[Rejection]int
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// Micrometer precision stabilizes boundary comparisons after projection.
func meters(v float64) float64 { return math.Round(v*1e6) / 1e6 }
