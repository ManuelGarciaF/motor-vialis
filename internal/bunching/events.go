package bunching

import (
	"fmt"
	"math"
	"sort"
	"time"
)

type Policy struct {
	DistanceMeters                float64
	MaximumAge, MaximumFutureSkew time.Duration
	// MinimumDuration filters API reads, not event capture or continuity.
	// Zero disables the duration filter; negative values are invalid.
	MinimumDuration time.Duration
	// MinimumSpeedMetersPerSecond filters reads using each vehicle's maximum
	// speed during accepted encounter samples. Zero disables this filter.
	MinimumSpeedMetersPerSecond                  float64
	MissingCaptureTolerance, MinimumObservations int
}

func (p Policy) Validate() error {
	if math.IsNaN(p.DistanceMeters) ||
		math.IsInf(p.DistanceMeters, 0) ||
		p.DistanceMeters <= 0 ||
		p.MaximumAge <= 0 ||
		p.MaximumFutureSkew < 0 ||
		p.MissingCaptureTolerance < 0 ||
		p.MinimumObservations < 2 ||
		p.MinimumDuration < 0 ||
		math.IsNaN(p.MinimumSpeedMetersPerSecond) ||
		math.IsInf(p.MinimumSpeedMetersPerSecond, 0) ||
		p.MinimumSpeedMetersPerSecond < 0 {
		return fmt.Errorf("invalid bunching policy")
	}
	return nil
}

// Scope keeps provider branches distinct even when they share a GTFS target.
// ShapeHash prevents continuity across geometrically different catalog versions.
type Scope struct {
	GCBARouteID   string `json:"gcbaRouteId"`
	GCBADirection int16  `json:"gcbaDirection"`
	GTFSRouteID   string `json:"gtfsRouteId"`
	GTFSDirection int16  `json:"gtfsDirectionId"`
	ShapeID       string `json:"shapeId"`
	ShapeHash     string `json:"shapeHash"`
}

type Point struct {
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}
type Observation struct {
	Line, Branch         string
	Scope                Scope
	VehicleID            string
	ObservedAt           time.Time
	Point                Point
	SpeedMetersPerSecond float64
}
type PairKey struct {
	Scope Scope
	A, B  string
}

func Key(scope Scope, a, b string) PairKey {
	if b < a {
		a, b = b, a
	}
	return PairKey{scope, a, b}
}

type Event struct {
	Line                         string     `json:"line"`
	Branch                       string     `json:"branch"`
	ID                           int64      `json:"id"`
	Scope                        Scope      `json:"scope"`
	VehicleA                     string     `json:"vehicleA"`
	VehicleB                     string     `json:"vehicleB"`
	FirstObservedAt              time.Time  `json:"firstObservedAt"`
	LastObservedAt               time.Time  `json:"lastObservedAt"`
	ConfirmedAt                  time.Time  `json:"lastConfirmedCaptureAt"`
	ObservedA                    time.Time  `json:"observedAAt"`
	ObservedB                    time.Time  `json:"observedBAt"`
	ClosedAt                     *time.Time `json:"-"`
	CloseReason                  string     `json:"-"`
	MinimumDistanceMeters        float64    `json:"minimumDistanceMeters"`
	LastDistanceMeters           float64    `json:"distanceMeters"`
	PointA                       Point      `json:"positionA"`
	PointB                       Point      `json:"positionB"`
	Observations                 int        `json:"observations"`
	MissingCaptures              int        `json:"-"`
	MaximumSpeedAMetersPerSecond float64    `json:"maximumSpeedAMetersPerSecond"`
	MaximumSpeedBMetersPerSecond float64    `json:"maximumSpeedBMetersPerSecond"`
}

func (e Event) key() PairKey { return Key(e.Scope, e.VehicleA, e.VehicleB) }
func minimumTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maximumTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

type PreviousSample struct{ ObservedA, ObservedB time.Time }

// Snapshot contains all fresh mapped positions in the response, including raw
// duplicates. Distances includes near pairs and fresh, possibly separated open
// pairs measured by the geographic repository. No groups are constructed.
type Snapshot struct {
	ReceivedAt               time.Time
	Partial, HasNewPositions bool
	Positions                map[string]Observation
	Distances                map[PairKey]float64
	Previous                 map[PairKey]PreviousSample
}

// Advance never extends a confirmation unless BOTH vehicle timestamps advance.
// Missing/expired closures record loss of evidence, not proven separation.
func Advance(open []Event, s Snapshot, p Policy) ([]Event, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if s.ReceivedAt.IsZero() {
		return nil, fmt.Errorf("snapshot time is required")
	}

	changes := make([]Event, 0)
	if s.Partial {
		return changes, nil
	}

	blocked := make(map[PairKey]bool)
	for _, event := range open {
		if event.ClosedAt != nil {
			continue
		}

		key := event.key()
		blocked[key] = true
		a, hasA := s.Positions[event.VehicleA]
		b, hasB := s.Positions[event.VehicleB]
		fresh := func(o Observation, ok bool) bool {
			return ok && !o.ObservedAt.Before(s.ReceivedAt.Add(-p.MaximumAge)) && !o.ObservedAt.After(s.ReceivedAt.Add(p.MaximumFutureSkew))
		}
		hasA = fresh(a, hasA)
		hasB = fresh(b, hasB)
		sameA := hasA && a.Scope == event.Scope
		sameB := hasB && b.Scope == event.Scope
		advances := sameA && sameB && a.ObservedAt.After(event.ObservedA) && b.ObservedAt.After(event.ObservedB)
		lastConfirmation := event.ConfirmedAt
		if lastConfirmation.IsZero() {
			lastConfirmation = event.LastObservedAt
		}
		observationGap := advances && (a.ObservedAt.Sub(event.ObservedA) > p.MaximumAge || b.ObservedAt.Sub(event.ObservedB) > p.MaximumAge)
		close := func(reason string) {
			now := s.ReceivedAt
			if reason == "expired" {
				now = lastConfirmation.Add(p.MaximumAge)
				if observationGap {
					now = minimumTime(now, minimumTime(event.ObservedA, event.ObservedB).Add(p.MaximumAge))
				}
			}
			// GPS clocks may lead reception by the allowed future skew.
			if now.Before(event.LastObservedAt) {
				now = event.LastObservedAt
			}
			event.ClosedAt = &now
			event.CloseReason = reason
			changes = append(changes, event)
		}
		if s.ReceivedAt.Sub(lastConfirmation) > p.MaximumAge || observationGap {
			close("expired")
			if advances {
				blocked[key] = false
			}
			continue
		}
		if hasA &&
			a.Scope != event.Scope &&
			a.ObservedAt.After(event.ObservedA) ||
			hasB &&
				b.Scope != event.Scope &&
				b.ObservedAt.After(event.ObservedB) {
			close("identity_changed")
			continue
		}

		distance, measured := s.Distances[key]
		if sameA && sameB && measured {
			if advances {
				if distance > p.DistanceMeters {
					close("separated")
					continue
				}

				event.ObservedA = a.ObservedAt
				event.ObservedB = b.ObservedAt
				event.LastObservedAt = maximumTime(a.ObservedAt, b.ObservedAt)
				event.ConfirmedAt = s.ReceivedAt
				event.LastDistanceMeters = distance
				event.MinimumDistanceMeters = math.Min(event.MinimumDistanceMeters, distance)
				event.PointA = a.Point
				event.PointB = b.Point
				event.MaximumSpeedAMetersPerSecond = math.Max(event.MaximumSpeedAMetersPerSecond, a.SpeedMetersPerSecond)
				event.MaximumSpeedBMetersPerSecond = math.Max(event.MaximumSpeedBMetersPerSecond, b.SpeedMetersPerSecond)
				event.Observations++
				event.MissingCaptures = 0
				changes = append(changes, event)
			}
		} else if s.HasNewPositions {
			event.MissingCaptures++
			if event.MissingCaptures > p.MissingCaptureTolerance {
				close("missing")
			} else {
				changes = append(changes, event)
			}
		}
	}

	for key, distance := range s.Distances {
		if !s.HasNewPositions || blocked[key] || distance > p.DistanceMeters || math.IsNaN(distance) || math.IsInf(distance, 0) {
			continue
		}

		a, hasA := s.Positions[key.A]
		b, hasB := s.Positions[key.B]
		if !hasA ||
			!hasB ||
			a.Scope != key.Scope ||
			b.Scope != key.Scope ||
			a.ObservedAt.Before(s.ReceivedAt.Add(-p.MaximumAge)) ||
			b.ObservedAt.Before(s.ReceivedAt.Add(-p.MaximumAge)) ||
			a.ObservedAt.After(s.ReceivedAt.Add(p.MaximumFutureSkew)) ||
			b.ObservedAt.After(s.ReceivedAt.Add(p.MaximumFutureSkew)) {
			continue
		}
		if previous, exists := s.Previous[key]; exists && (!a.ObservedAt.After(previous.ObservedA) || !b.ObservedAt.After(previous.ObservedB)) {
			continue
		}

		observed := maximumTime(a.ObservedAt, b.ObservedAt)
		changes = append(changes, Event{Line: a.Line, Branch: a.Branch, Scope: key.Scope, VehicleA: key.A, VehicleB: key.B, FirstObservedAt: observed, LastObservedAt: observed, ConfirmedAt: s.ReceivedAt, ObservedA: a.ObservedAt, ObservedB: b.ObservedAt, MinimumDistanceMeters: distance, LastDistanceMeters: distance, PointA: a.Point, PointB: b.Point, Observations: 1, MaximumSpeedAMetersPerSecond: a.SpeedMetersPerSecond, MaximumSpeedBMetersPerSecond: b.SpeedMetersPerSecond})
	}

	sort.SliceStable(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if (a.ID > 0) != (b.ID > 0) {
			return a.ID > 0
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return fmt.Sprintf("%#v", a.key()) < fmt.Sprintf("%#v", b.key())
	})
	return changes, nil
}

// Midpoint computes a spherical midpoint rather than averaging longitudes.
func Midpoint(a, b Point) Point {
	rad := math.Pi / 180
	latA, lonA, latB, lonB := a.Latitude*rad, a.Longitude*rad, b.Latitude*rad, b.Longitude*rad
	x := math.Cos(latA)*math.Cos(lonA) + math.Cos(latB)*math.Cos(lonB)
	y := math.Cos(latA)*math.Sin(lonA) + math.Cos(latB)*math.Sin(lonB)
	z := math.Sin(latA) + math.Sin(latB)
	return Point{Longitude: math.Atan2(y, x) / rad, Latitude: math.Atan2(z, math.Hypot(x, y)) / rad}
}
