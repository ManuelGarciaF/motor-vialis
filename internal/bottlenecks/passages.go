package bottlenecks

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Measure reconstructs complete passages from a batch of projected observations.
// Input order is irrelevant and the caller's slice is not mutated. Output order
// is vehicle ID, crossing time, sector index. It never extrapolates missing
// entry/exit evidence or extends time beyond the final observation.
// Batch completion censors pending entries; the producer finalizes sequences
// with the same measurement cursor and an explicit closure reason.
func Measure(layout Layout, observations []Observation, policy MeasurementPolicy) (Measurements, error) {
	if len(layout.sectors) == 0 {
		return Measurements{}, fmt.Errorf("uninitialized layout")
	}
	if err := policy.validate(); err != nil {
		return Measurements{}, err
	}

	ordered := append([]Observation(nil), observations...)
	for i, o := range ordered {
		if strings.TrimSpace(o.VehicleID) == "" ||
			o.ObservedAt.IsZero() ||
			!finite(o.ProgressMeters) ||
			!finite(o.DistanceToShapeMeters) ||
			o.DistanceToShapeMeters < 0 {
			return Measurements{}, fmt.Errorf("invalid observation at index %d", i)
		}

		switch o.Match {
		case Matched:
			if err := o.Scope.validate(); err != nil {
				return Measurements{}, fmt.Errorf("observation at index %d: %w", i, err)
			}
		case Unmatched, Ambiguous:
		default:
			return Measurements{}, fmt.Errorf("invalid match status at index %d", i)
		}
	}

	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].VehicleID != ordered[j].VehicleID {
			return ordered[i].VehicleID < ordered[j].VehicleID
		}
		return ordered[i].ObservedAt.Before(ordered[j].ObservedAt)
	})
	result := Measurements{Passages: []Passage{}, Incomplete: []IncompletePassage{}, Rejections: make(map[Rejection]int)}
	unique := ordered[:0]
	for _, o := range ordered {
		if len(unique) > 0 {
			last := unique[len(unique)-1]
			if last.VehicleID == o.VehicleID && last.ObservedAt.Equal(o.ObservedAt) {
				if !sameObservation(last, o) {
					return Measurements{}, fmt.Errorf("conflicting observations for the same vehicle/timestamp")
				}

				result.Duplicates++
				continue
			}
		}
		unique = append(unique, o)
	}

	cursor := measurementCursor{}
	for _, o := range unique {
		cursor.advance(layout, o, policy, &result)
	}

	cursor.abandon(BatchBoundary, &result)
	return result, nil
}

type measurementCursor struct {
	previous *Observation
	pending  *passageStart
}

func (c *measurementCursor) abandon(reason Rejection, result *Measurements) {
	if c.pending != nil && c.previous != nil {
		result.Incomplete = append(result.Incomplete, IncompletePassage{
			VehicleID: c.previous.VehicleID, Scope: c.previous.Scope,
			Sector: c.pending.sector, Entered: c.pending.crossing, Reason: reason,
		})
	}
	c.pending = nil
}

func (c *measurementCursor) advance(layout Layout, o Observation, policy MeasurementPolicy, result *Measurements) {
	previous, pending := c.previous, c.pending
	defer func() { c.previous, c.pending = previous, pending }()

	abandon := func(reason Rejection) {
		c.previous, c.pending = previous, pending
		c.abandon(reason, result)
		pending = nil
	}

	if previous != nil && previous.VehicleID != o.VehicleID {
		abandon(BatchBoundary)
		previous = nil
	}
	if reason := rejectObservation(layout, o, policy); reason != "" {
		abandon(reason)
		result.Rejections[reason]++
		previous, pending = nil, nil
		return
	}

	o.ProgressMeters = meters(o.ProgressMeters)
	if previous == nil || previous.VehicleID != o.VehicleID {
		pending = startAtBoundary(layout, o)
		previous = &o
		return
	}
	if reason := rejectEdge(*previous, o, policy); reason != "" {
		abandon(reason)
		result.Rejections[reason]++
		pending = startAtBoundary(layout, o)
		previous = &o
		return
	}

	gap := o.ObservedAt.Sub(previous.ObservedAt)
	if pending != nil {
		pending.maximumGap = max(pending.maximumGap, gap)
	}
	if o.ProgressMeters > previous.ProgressMeters {
		// Search only boundaries actually crossed, rather than every sector.
		first := sort.Search(len(layout.sectors)+1, func(i int) bool {
			return boundary(layout, i) > previous.ProgressMeters
		})
		for i := first; i <= len(layout.sectors) && boundary(layout, i) <= o.ProgressMeters; i++ {
			crossed := interpolate(*previous, o, boundary(layout, i))
			if pending != nil && pending.sector.Index == i-1 {
				duration := crossed.At.Sub(pending.crossing.At)
				if duration > 0 {
					result.Passages = append(result.Passages, Passage{
						VehicleID: o.VehicleID, Scope: o.Scope, Sector: pending.sector,
						Entered: pending.crossing, Exited: crossed, Duration: duration,
						SpeedKPH:             pending.sector.LengthMeters() / duration.Seconds() * 3.6,
						InteriorObservations: pending.interior,
						MaximumSupportingGap: max(pending.maximumGap, crossed.AfterAt.Sub(crossed.BeforeAt)),
						SameGPSInterval:      pending.crossing.BeforeAt.Equal(crossed.BeforeAt) && pending.crossing.AfterAt.Equal(crossed.AfterAt),
					})
				} else {
					abandon(DegenerateCrossing)
				}
			}
			pending = nil
			if i < len(layout.sectors) {
				pending = &passageStart{sector: layout.sectors[i], crossing: crossed, maximumGap: crossed.AfterAt.Sub(crossed.BeforeAt)}
			}
		}
	}
	if pending != nil && o.ProgressMeters > pending.sector.StartMeters && o.ProgressMeters < pending.sector.EndMeters {
		pending.interior++
	}
	previous = &o
}

type passageStart struct {
	sector     Sector
	crossing   Crossing
	interior   int
	maximumGap time.Duration
}

func boundary(l Layout, i int) float64 {
	if i == len(l.sectors) {
		return l.length
	}
	return l.sectors[i].StartMeters
}

func startAtBoundary(l Layout, o Observation) *passageStart {
	i := sort.Search(len(l.sectors), func(i int) bool { return l.sectors[i].StartMeters >= o.ProgressMeters })
	if i < len(l.sectors) && l.sectors[i].StartMeters == o.ProgressMeters {
		return &passageStart{sector: l.sectors[i], crossing: exactCrossing(o.ObservedAt)}
	}
	return nil
}

func exactCrossing(at time.Time) Crossing { return Crossing{At: at, BeforeAt: at, AfterAt: at} }

func interpolate(a, b Observation, progress float64) Crossing {
	if progress == b.ProgressMeters {
		return exactCrossing(b.ObservedAt)
	}

	fraction := (progress - a.ProgressMeters) / (b.ProgressMeters - a.ProgressMeters)
	return Crossing{
		At:       a.ObservedAt.Add(time.Duration(float64(b.ObservedAt.Sub(a.ObservedAt)) * fraction)),
		BeforeAt: a.ObservedAt, AfterAt: b.ObservedAt,
	}
}

func rejectObservation(l Layout, o Observation, p MeasurementPolicy) Rejection {
	if o.Match != Matched {
		return UnresolvedMatch
	}
	if o.Scope.Version != l.version {
		return WrongVersion
	}
	if o.DistanceToShapeMeters > p.MaximumDistanceMeters {
		return OutsideShape
	}

	progress := meters(o.ProgressMeters)
	if progress < 0 || progress > l.length {
		return OutsideProgress
	}
	return ""
}

func rejectEdge(a, b Observation, p MeasurementPolicy) Rejection {
	if a.Scope != b.Scope {
		return ScopeChanged
	}

	gap := b.ObservedAt.Sub(a.ObservedAt)
	if gap > p.MaximumGap {
		return GPSGap
	}

	delta := b.ProgressMeters - a.ProgressMeters
	if delta < 0 {
		return Backwards
	}
	if delta/gap.Seconds() > p.MaximumSpeedKPH/3.6 {
		return ImplausibleSpeed
	}
	return ""
}

func sameObservation(a, b Observation) bool {
	return a.Scope == b.Scope && a.Match == b.Match && a.ProgressMeters == b.ProgressMeters && a.DistanceToShapeMeters == b.DistanceToShapeMeters
}
