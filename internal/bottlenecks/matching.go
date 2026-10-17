package bottlenecks

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Projection is one distinct local closest point on a versioned shape.
// Distances and progress must be geodesic, consistently with the layout.
type Projection struct {
	ProgressMeters, DistanceMeters float64
}

// CandidateObservation contains all local projection alternatives within the
// search radius. An empty list means no spatial match, not absence of a bus.
type CandidateObservation struct {
	VehicleID  string
	Scope      Scope
	ObservedAt time.Time
	Candidates []Projection
}

type Matching struct {
	Observations                                              []Observation
	Duplicates                                                int
	SpatiallyUnique, TemporallyResolved, Ambiguous, Unmatched int
}

// MatchConservatively never chooses the nearest alternative as a tie-break.
// Spatial singletons are anchors. Multiple alternatives can be resolved only by
// compatible immediate predecessor/successor anchors within the same identity
// and maximum GPS gap. Resolution propagates in both directions until stable.
// Empty or ambiguous observations remain barriers to measuring passages.
// This is deliberately not a full path optimizer: unsupported ambiguity stays
// unresolved. Contradictory singleton anchors are retained for Measure to reject
// the edge; they do not justify inventing another position.
func MatchConservatively(layout Layout, input []CandidateObservation, policy MeasurementPolicy) (Matching, error) {
	if len(layout.sectors) == 0 {
		return Matching{}, fmt.Errorf("uninitialized layout")
	}
	if err := policy.validate(); err != nil {
		return Matching{}, err
	}

	rows := append([]CandidateObservation(nil), input...)
	for i := range rows {
		r := &rows[i]
		if strings.TrimSpace(r.VehicleID) == "" || r.ObservedAt.IsZero() {
			return Matching{}, fmt.Errorf("invalid candidate observation")
		}
		if err := r.Scope.validate(); err != nil {
			return Matching{}, err
		}

		candidates := make([]Projection, 0, len(r.Candidates))
		for _, c := range r.Candidates {
			if !finite(c.ProgressMeters) || !finite(c.DistanceMeters) || c.DistanceMeters < 0 {
				return Matching{}, fmt.Errorf("invalid projection")
			}

			c.ProgressMeters = meters(c.ProgressMeters)
			if r.Scope.Version == layout.version &&
				c.ProgressMeters >= 0 &&
				c.ProgressMeters <= layout.length &&
				c.DistanceMeters <= policy.MaximumDistanceMeters {
				candidates = append(candidates, c)
			}
		}

		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].ProgressMeters != candidates[j].ProgressMeters {
				return candidates[i].ProgressMeters < candidates[j].ProgressMeters
			}
			return candidates[i].DistanceMeters < candidates[j].DistanceMeters
		})
		r.Candidates = candidates[:0]
		for _, c := range candidates {
			if len(r.Candidates) == 0 || r.Candidates[len(r.Candidates)-1].ProgressMeters != c.ProgressMeters {
				r.Candidates = append(r.Candidates, c)
			}
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].VehicleID != rows[j].VehicleID {
			return rows[i].VehicleID < rows[j].VehicleID
		}
		return rows[i].ObservedAt.Before(rows[j].ObservedAt)
	})
	result := Matching{Observations: []Observation{}}
	unique := rows[:0]
	for _, r := range rows {
		if len(unique) > 0 {
			last := unique[len(unique)-1]
			if last.VehicleID == r.VehicleID && last.ObservedAt.Equal(r.ObservedAt) {
				if last.Scope != r.Scope || !reflect.DeepEqual(last.Candidates, r.Candidates) {
					return Matching{}, fmt.Errorf("conflicting candidate observations for vehicle/timestamp")
				}

				result.Duplicates++
				continue
			}
		}
		unique = append(unique, r)
	}

	initial := make([]int, len(unique))
	for i := range unique {
		initial[i] = len(unique[i].Candidates)
	}

	for changed := true; changed; {
		changed = false
		for i := range unique {
			row := &unique[i]
			if len(row.Candidates) < 2 {
				continue
			}

			var before, after *CandidateObservation
			if i > 0 && anchorAdjacent(unique[i-1], *row, policy) {
				before = &unique[i-1]
			}
			if i+1 < len(unique) && anchorAdjacent(unique[i+1], *row, policy) {
				after = &unique[i+1]
			}
			if before == nil && after == nil {
				continue
			}

			kept := row.Candidates[:0]
			for _, c := range row.Candidates {
				if before != nil && !reachable(before.Candidates[0].ProgressMeters, c.ProgressMeters, row.ObservedAt.Sub(before.ObservedAt), policy) {
					continue
				}
				if after != nil && !reachable(c.ProgressMeters, after.Candidates[0].ProgressMeters, after.ObservedAt.Sub(row.ObservedAt), policy) {
					continue
				}

				kept = append(kept, c)
			}

			if len(kept) != len(row.Candidates) {
				changed = true
			}
			row.Candidates = kept
		}
	}

	for i, r := range unique {
		o := Observation{VehicleID: r.VehicleID, Scope: r.Scope, ObservedAt: r.ObservedAt}
		switch len(r.Candidates) {
		case 0:
			result.Unmatched++
		case 1:
			o.Match = Matched
			o.ProgressMeters = r.Candidates[0].ProgressMeters
			o.DistanceToShapeMeters = r.Candidates[0].DistanceMeters
			if initial[i] == 1 {
				result.SpatiallyUnique++
			} else {
				result.TemporallyResolved++
			}
		default:
			o.Match = Ambiguous
			result.Ambiguous++
		}

		result.Observations = append(result.Observations, o)
	}

	return result, nil
}

func anchorAdjacent(anchor, row CandidateObservation, p MeasurementPolicy) bool {
	gap := anchor.ObservedAt.Sub(row.ObservedAt)
	if gap < 0 {
		gap = -gap
	}
	return len(anchor.Candidates) == 1 && anchor.VehicleID == row.VehicleID && anchor.Scope == row.Scope && gap > 0 && gap <= p.MaximumGap
}

func reachable(from, to float64, gap time.Duration, p MeasurementPolicy) bool {
	return to >= from && (to-from)/gap.Seconds() <= p.MaximumSpeedKPH/3.6
}
