package bottlenecks

import (
	"sort"
	"time"
)

// Coverage describes observed evidence, not eligibility or recurrent slowness.
type Coverage struct {
	TimeZone, PassageAttribution                                                           string
	TotalSectors, MatchedGPSPositions, ValidPassages                                       int
	StartedMeasurements, IncompleteMeasurements, BoundaryCensoredMeasurements              int
	SectorsWithMatchedGPS, SectorsWithValidPassages, SectorsWithEvidenceButNoValidPassages int
	SectorsWithPassagesOnMultipleDates, SectorHourPairsWithPassagesOnMultipleDates         int
	CellsWithValidPassages                                                                 int
	IncompleteByReason                                                                     map[Rejection]int
	Sectors                                                                                []SectorCoverage
	Cells                                                                                  []CoverageCell
}

type SectorCoverage struct {
	SectorIndex                                                                  int
	MatchedGPSPositions, UniqueGPSVehicles, ValidPassages, UniquePassageVehicles int
	IncompleteMeasurements                                                       int
	DatesWithPassages                                                            []string
}

type CoverageCell struct {
	SectorIndex                                                                  int
	Date                                                                         string
	Hour                                                                         int
	MatchedGPSPositions, UniqueGPSVehicles, ValidPassages, UniquePassageVehicles int
	IncompleteMeasurements                                                       int
	IncompleteByReason                                                           map[Rejection]int
	InteriorGPSPassages, SameIntervalPassages, BoundaryOnlyPassages              int
}

type coverageUnits struct {
	gps, passages map[string]struct{}
}

func newCoverageUnits() coverageUnits {
	return coverageUnits{gps: make(map[string]struct{}), passages: make(map[string]struct{})}
}

// buildCoverage attributes measurements once to estimated entry, while GPS
// exposure belongs to observation time. Empty cells do not imply service absence.
func buildCoverage(layout Layout, observations []Observation, measured Measurements, location *time.Location) Coverage {
	c := Coverage{
		TimeZone: location.String(), PassageAttribution: "estimated_entry",
		TotalSectors: len(layout.sectors), ValidPassages: len(measured.Passages),
		IncompleteMeasurements: len(measured.Incomplete),
		StartedMeasurements:    len(measured.Passages) + len(measured.Incomplete),
		IncompleteByReason:     make(map[Rejection]int),
		Sectors:                make([]SectorCoverage, len(layout.sectors)), Cells: make([]CoverageCell, 0),
	}
	sectorUnits := make([]coverageUnits, len(c.Sectors))
	dates := make([]map[string]struct{}, len(c.Sectors))
	for i := range c.Sectors {
		c.Sectors[i] = SectorCoverage{SectorIndex: i, DatesWithPassages: make([]string, 0)}
		sectorUnits[i] = newCoverageUnits()
		dates[i] = make(map[string]struct{})
	}
	type cellKey struct {
		sector int
		date   string
		hour   int
	}
	cells := make(map[cellKey]*CoverageCell)
	units := make(map[cellKey]coverageUnits)
	cellFor := func(index int, at time.Time) (*CoverageCell, coverageUnits) {
		local := at.In(location)
		key := cellKey{index, local.Format("2006-01-02"), local.Hour()}
		cell := cells[key]
		if cell == nil {
			cell = &CoverageCell{SectorIndex: index, Date: key.date, Hour: key.hour, IncompleteByReason: make(map[Rejection]int)}
			cells[key] = cell
			units[key] = newCoverageUnits()
		}
		return cell, units[key]
	}
	type observationKey struct {
		vehicle string
		at      time.Time
	}
	seen := make(map[observationKey]struct{})
	for _, o := range observations {
		if o.Match != Matched || o.Scope.Version != layout.version || !finite(o.ProgressMeters) || o.ProgressMeters < 0 || o.ProgressMeters > layout.length {
			continue
		}
		key := observationKey{o.VehicleID, o.ObservedAt.UTC()}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		index := sort.Search(len(layout.sectors), func(i int) bool { return o.ProgressMeters < layout.sectors[i].EndMeters })
		if index == len(layout.sectors) {
			index--
		}
		cell, u := cellFor(index, o.ObservedAt)
		cell.MatchedGPSPositions++
		u.gps[o.VehicleID] = struct{}{}
		c.Sectors[index].MatchedGPSPositions++
		sectorUnits[index].gps[o.VehicleID] = struct{}{}
		c.MatchedGPSPositions++
	}
	for _, p := range measured.Passages {
		index := p.Sector.Index
		cell, u := cellFor(index, p.Entered.At)
		cell.ValidPassages++
		u.passages[p.VehicleID] = struct{}{}
		if p.InteriorObservations > 0 {
			cell.InteriorGPSPassages++
		} else if p.SameGPSInterval {
			cell.SameIntervalPassages++
		} else {
			cell.BoundaryOnlyPassages++
		}
		c.Sectors[index].ValidPassages++
		sectorUnits[index].passages[p.VehicleID] = struct{}{}
		dates[index][cell.Date] = struct{}{}
	}
	for _, p := range measured.Incomplete {
		cell, _ := cellFor(p.Sector.Index, p.Entered.At)
		cell.IncompleteMeasurements++
		cell.IncompleteByReason[p.Reason]++
		c.Sectors[p.Sector.Index].IncompleteMeasurements++
		c.IncompleteByReason[p.Reason]++
		if p.Reason == BatchBoundary {
			c.BoundaryCensoredMeasurements++
		}
	}
	for i := range c.Sectors {
		s := &c.Sectors[i]
		s.UniqueGPSVehicles = len(sectorUnits[i].gps)
		s.UniquePassageVehicles = len(sectorUnits[i].passages)
		for date := range dates[i] {
			s.DatesWithPassages = append(s.DatesWithPassages, date)
		}
		sort.Strings(s.DatesWithPassages)
		if s.MatchedGPSPositions > 0 {
			c.SectorsWithMatchedGPS++
		}
		if s.ValidPassages > 0 {
			c.SectorsWithValidPassages++
		} else if s.MatchedGPSPositions > 0 || s.IncompleteMeasurements > 0 {
			c.SectorsWithEvidenceButNoValidPassages++
		}
		if len(s.DatesWithPassages) > 1 {
			c.SectorsWithPassagesOnMultipleDates++
		}
	}
	type hourKey struct{ sector, hour int }
	hourDates := make(map[hourKey]int)
	for key, cell := range cells {
		cell.UniqueGPSVehicles = len(units[key].gps)
		cell.UniquePassageVehicles = len(units[key].passages)
		if cell.ValidPassages > 0 {
			c.CellsWithValidPassages++
			hourDates[hourKey{cell.SectorIndex, cell.Hour}]++
		}
		c.Cells = append(c.Cells, *cell)
	}
	for _, count := range hourDates {
		if count > 1 {
			c.SectorHourPairsWithPassagesOnMultipleDates++
		}
	}
	sort.Slice(c.Cells, func(i, j int) bool {
		a, b := c.Cells[i], c.Cells[j]
		if a.SectorIndex != b.SectorIndex {
			return a.SectorIndex < b.SectorIndex
		}
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		return a.Hour < b.Hour
	})
	return c
}
