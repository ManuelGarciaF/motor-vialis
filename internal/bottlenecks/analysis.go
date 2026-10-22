package bottlenecks

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

var ErrLineNotFound = errors.New("analysis line not found")

type Window struct{ From, To time.Time }

func (w Window) Validate() error {
	if w.From.IsZero() || w.To.IsZero() || !w.From.Before(w.To) {
		return fmt.Errorf("invalid analysis window: require nonzero from < to")
	}
	return nil
}

type Dataset struct {
	LineID       int64
	Version      ShapeVersion
	LengthMeters float64
	Line, Branch string
	Observations []CandidateObservation
}

type Repository interface {
	// Load reads a single consistent snapshot, without writing source data.
	Load(context.Context, int64, Window, float64) (Dataset, error)
}

type AnalysisRequest struct {
	LineID        int64
	Window        Window
	NominalMeters float64
	Policy        MeasurementPolicy
	TimeZone      string
}

type SectorMeasurement struct {
	Sector                                              Sector
	Passages, InteriorGPSPassages, SameIntervalPassages int
	MedianDurationSeconds, TypicalSpeedKPH              *float64
}

type AnalysisLimits struct {
	MaximumGapSeconds     float64
	MaximumDistanceMeters float64
	MaximumSpeedKPH       float64
}

// Analysis is an experimental CLI report, not the future HTTP contract.
// It intentionally excludes vehicle IDs and raw timestamps/coordinates.
type Analysis struct {
	LineID                                                                              int64
	Version                                                                             ShapeVersion
	Line, Branch                                                                        string
	Window                                                                              Window
	Policy                                                                              AnalysisLimits
	NominalMeters, LengthMeters                                                         float64
	DistanceMethod                                                                      string
	RawPositions, SpatiallyUnique, TemporallyResolved, Ambiguous, Unmatched, Duplicates int
	Rejections                                                                          map[Rejection]int
	Sectors                                                                             []SectorMeasurement
	Coverage                                                                            Coverage
}

func (request AnalysisRequest) Validate() error {
	if request.LineID <= 0 {
		return fmt.Errorf("line ID must be positive")
	}
	if err := request.Window.Validate(); err != nil {
		return err
	}
	if err := request.Policy.validate(); err != nil {
		return err
	}
	if !finite(request.NominalMeters) || request.NominalMeters <= 0 {
		return fmt.Errorf("invalid nominal length")
	}
	if _, err := time.LoadLocation(request.TimeZone); err != nil || request.TimeZone == "" {
		return fmt.Errorf("invalid coverage timezone")
	}
	return nil
}

func Analyze(ctx context.Context, repo Repository, request AnalysisRequest) (Analysis, error) {
	if err := request.Validate(); err != nil {
		return Analysis{}, err
	}

	location, err := time.LoadLocation(request.TimeZone)
	if err != nil {
		return Analysis{}, fmt.Errorf("load coverage timezone: %w", err)
	}

	data, err := repo.Load(ctx, request.LineID, request.Window, request.Policy.MaximumDistanceMeters)
	if err != nil {
		return Analysis{}, err
	}

	layout, err := NewLayout(data.Version, data.LengthMeters, request.NominalMeters)
	if err != nil {
		return Analysis{}, err
	}

	matching, err := MatchConservatively(layout, data.Observations, request.Policy)
	if err != nil {
		return Analysis{}, err
	}

	measured, err := Measure(layout, matching.Observations, request.Policy)
	if err != nil {
		return Analysis{}, err
	}

	result := Analysis{LineID: data.LineID, Version: data.Version, Line: data.Line, Branch: data.Branch, Window: request.Window, Policy: AnalysisLimits{MaximumGapSeconds: request.Policy.MaximumGap.Seconds(), MaximumDistanceMeters: request.Policy.MaximumDistanceMeters, MaximumSpeedKPH: request.Policy.MaximumSpeedKPH},
		NominalMeters: layout.NominalMeters(), LengthMeters: layout.LengthMeters(), DistanceMethod: "PostGIS geography (spheroid)",
		RawPositions: len(data.Observations), SpatiallyUnique: matching.SpatiallyUnique, TemporallyResolved: matching.TemporallyResolved,
		Ambiguous: matching.Ambiguous, Unmatched: matching.Unmatched, Duplicates: matching.Duplicates + measured.Duplicates,
		Rejections: measured.Rejections, Sectors: make([]SectorMeasurement, len(layout.sectors))}
	durations := make([][]float64, len(layout.sectors))
	for i, s := range layout.sectors {
		result.Sectors[i].Sector = s
	}

	for _, p := range measured.Passages {
		s := &result.Sectors[p.Sector.Index]
		s.Passages++
		if p.InteriorObservations > 0 {
			s.InteriorGPSPassages++
		}
		if p.SameGPSInterval {
			s.SameIntervalPassages++
		}
		durations[p.Sector.Index] = append(durations[p.Sector.Index], p.Duration.Seconds())
	}

	for i, d := range durations {
		if len(d) == 0 {
			continue
		}

		sort.Float64s(d)
		median := d[len(d)/2]
		if len(d)%2 == 0 {
			median = (d[len(d)/2-1] + median) / 2
		}
		speed := result.Sectors[i].Sector.LengthMeters() / median * 3.6
		result.Sectors[i].MedianDurationSeconds = &median
		result.Sectors[i].TypicalSpeedKPH = &speed
	}

	result.Coverage = buildCoverage(layout, matching.Observations, measured, location)
	return result, nil
}
