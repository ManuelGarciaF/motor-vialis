package bottlenecks

import (
	"testing"
	"time"
)

func coverageLocation(t *testing.T) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestCoverageIncludesPassagesWithoutInteriorGPS(t *testing.T) {
	layout := testLayout(t, 1500, 500)
	obs := []Observation{observation(0, 250), observation(120, 1250)}
	measured := measure(t, layout, obs...)
	c := buildCoverage(layout, obs, measured, coverageLocation(t))
	if c.ValidPassages != 1 || c.IncompleteMeasurements != 1 || c.BoundaryCensoredMeasurements != 1 || c.StartedMeasurements != 2 {
		t.Fatalf("coverage %+v", c)
	}
	if c.SectorsWithMatchedGPS != 2 || c.SectorsWithValidPassages != 1 || c.SectorsWithEvidenceButNoValidPassages != 2 {
		t.Fatalf("GPS-only is not passage coverage: %+v", c)
	}
	for _, cell := range c.Cells {
		if cell.SectorIndex == 1 {
			if cell.ValidPassages != 1 || cell.SameIntervalPassages != 1 || cell.MatchedGPSPositions != 0 || cell.UniquePassageVehicles != 1 || cell.Hour != 9 || cell.Date != "2026-10-10" {
				t.Fatalf("no-interior cell %+v", cell)
			}
		}
	}
	if c.Sectors[1].UniqueGPSVehicles != 0 || c.Sectors[1].UniquePassageVehicles != 1 {
		t.Fatal("conflated GPS and passage exposure")
	}
}

func TestCoverageAttributesOnceToEntryAcrossMidnight(t *testing.T) {
	layout := testLayout(t, 1500, 500)
	entry := time.Date(2026, 10, 10, 2, 59, 30, 0, time.UTC) // 23:59:30 previous local date.
	p := Passage{VehicleID: "v", Sector: layout.sectors[1], Entered: exactCrossing(entry), Exited: exactCrossing(entry.Add(time.Minute)), Duration: time.Minute, InteriorObservations: 1}
	o := observation(0, 750)
	o.ObservedAt = entry.Add(45 * time.Second)
	c := buildCoverage(layout, []Observation{o}, Measurements{Passages: []Passage{p}}, coverageLocation(t))
	if c.ValidPassages != 1 || len(c.Cells) != 2 || c.CellsWithValidPassages != 1 {
		t.Fatalf("duplicated passage: %+v", c)
	}
	if c.Cells[0].Date != "2026-10-09" || c.Cells[0].Hour != 23 || c.Cells[0].ValidPassages != 1 || c.Cells[0].InteriorGPSPassages != 1 {
		t.Fatalf("entry attribution %+v", c.Cells)
	}
	if c.Cells[1].Date != "2026-10-10" || c.Cells[1].Hour != 0 || c.Cells[1].MatchedGPSPositions != 1 || c.Cells[1].ValidPassages != 0 {
		t.Fatalf("observation time %+v", c.Cells)
	}
	if len(c.Sectors[1].DatesWithPassages) != 1 || c.Sectors[1].DatesWithPassages[0] != "2026-10-09" {
		t.Fatal("wrong passage dates")
	}
}

func TestCoverageCountsDistinctVehiclesAndDatesNotRecurrence(t *testing.T) {
	l := testLayout(t, 1500, 500)
	p := Passage{VehicleID: "v", Sector: l.sectors[1], Entered: exactCrossing(epoch), Exited: exactCrossing(epoch.Add(time.Minute)), Duration: time.Minute}
	same := p
	same.Entered = exactCrossing(epoch.Add(10 * time.Minute))
	same.InteriorObservations = 2
	otherDay := p
	otherDay.Entered = exactCrossing(epoch.Add(24 * time.Hour))
	otherDay.SameGPSInterval = true
	c := buildCoverage(l, nil, Measurements{Passages: []Passage{p, same, otherDay}}, coverageLocation(t))
	if c.ValidPassages != 3 || c.Sectors[1].UniquePassageVehicles != 1 || c.Cells[0].UniquePassageVehicles != 1 {
		t.Fatalf("samples counted as vehicles %+v", c)
	}
	if c.SectorsWithPassagesOnMultipleDates != 1 || c.SectorHourPairsWithPassagesOnMultipleDates != 1 || len(c.Sectors[1].DatesWithPassages) != 2 {
		t.Fatalf("dates %+v", c)
	}
	if c.Cells[0].BoundaryOnlyPassages != 1 || c.Cells[0].InteriorGPSPassages != 1 || c.Cells[1].SameIntervalPassages != 1 {
		t.Fatalf("support categories %+v", c.Cells)
	}
}

func TestIncompletePassagesTrackSupportedEntryNotUnknownTrips(t *testing.T) {
	l := testLayout(t, 1500, 500)
	bad := observation(120, 850)
	bad.Match = Ambiguous
	obs := []Observation{observation(0, 250), observation(60, 750), bad, observation(180, 1250)}
	m := measure(t, l, obs...)
	if len(m.Passages) != 0 || len(m.Incomplete) != 1 || m.Incomplete[0].Reason != UnresolvedMatch || m.Incomplete[0].Sector.Index != 1 {
		t.Fatalf("measurement %+v", m)
	}
	c := buildCoverage(l, obs, m, coverageLocation(t))
	if c.StartedMeasurements != 1 || c.IncompleteByReason[UnresolvedMatch] != 1 || c.BoundaryCensoredMeasurements != 0 {
		t.Fatalf("supported start diagnostic %+v", c)
	}
	// Next vehicle's bad GPS must not be blamed for the previous vehicle's ending.
	next := observation(180, 0)
	next.Match = Unmatched
	next.VehicleID = "z"
	m = measure(t, l, observation(0, 250), observation(60, 750), next)
	if len(m.Incomplete) != 1 || m.Incomplete[0].Reason != BatchBoundary || m.Incomplete[0].VehicleID != "v" {
		t.Fatalf("vehicle boundary %+v", m)
	}
	// A single interior observation has no supported entry, so zero inferred starts.
	m = measure(t, l, observation(0, 750))
	if len(m.Passages) != 0 || len(m.Incomplete) != 0 {
		t.Fatal("invented an unobserved entry")
	}
}

func TestCoverageEmptyAndFinalEndpoint(t *testing.T) {
	l := testLayout(t, 1000, 500)
	c := buildCoverage(l, nil, Measurements{}, coverageLocation(t))
	if c.TotalSectors != 2 || len(c.Sectors) != 2 || len(c.Cells) != 0 || c.ValidPassages != 0 {
		t.Fatalf("empty coverage %+v", c)
	}
	c = buildCoverage(l, []Observation{observation(0, 1000)}, Measurements{}, coverageLocation(t))
	if c.Sectors[1].MatchedGPSPositions != 1 || c.SectorsWithValidPassages != 0 {
		t.Fatal("final endpoint exposure")
	}
}
