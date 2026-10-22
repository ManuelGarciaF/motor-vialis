package bottlenecks

import (
	"context"
	"fmt"
	"time"
)

// AnalysisTarget records the expected geometry/row count at selection time.
// List/read snapshots are separate: changes abort the batch, never silently
// mix a regenerated catalog or a changed observation population into the report.
type AnalysisTarget struct {
	LineID               int64
	Version              ShapeVersion
	ExpectedRawPositions int64
}

type TargetRepository interface {
	Repository
	ListTargets(context.Context, Window) ([]AnalysisTarget, error)
}

type BatchSummary struct {
	Targets, RawPositions                                                                  int
	Sectors, ValidPassages, IncompleteMeasurements, BoundaryCensoredMeasurements           int
	SectorsWithValidPassages, SectorsWithMatchedGPS, SectorsWithEvidenceButNoValidPassages int
	SectorsWithPassagesOnMultipleDates, SectorHourPairsWithPassagesOnMultipleDates         int
	CellsWithValidPassages                                                                 int
}

// AnalyzeTargets streams successful per-target results. A caller must publish a
// completion marker only on success; a partial stream is not a global result.
// A batch shares a fixed window/config but not one long-running DB transaction.
func AnalyzeTargets(ctx context.Context, repo Repository, targets []AnalysisTarget, request AnalysisRequest, emit func(Analysis) error) (BatchSummary, error) {
	var summary BatchSummary
	validation := request
	validation.LineID = 1 // Actual target IDs are validated independently below.
	if err := validation.Validate(); err != nil {
		return summary, err
	}
	if emit == nil {
		return summary, fmt.Errorf("analysis callback is required")
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}

	for _, target := range targets {
		if target.LineID <= 0 || target.ExpectedRawPositions < 0 {
			return summary, fmt.Errorf("invalid analysis target")
		}
		if err := target.Version.validate(); err != nil {
			return summary, err
		}
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		request.LineID = target.LineID
		// Bound each target independently; the caller owns the global run budget.
		targetCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		report, err := Analyze(targetCtx, repo, request)
		if err == nil {
			err = targetCtx.Err()
		}
		cancel()
		if err != nil {
			return summary, fmt.Errorf("analyze target %d: %w", target.LineID, err)
		}
		if report.Version != target.Version || int64(report.RawPositions) != target.ExpectedRawPositions {
			return summary, fmt.Errorf("target %d changed since selection; discard partial batch and retry", target.LineID)
		}
		if err := emit(report); err != nil {
			return summary, err
		}

		summary.Targets++
		summary.RawPositions += report.RawPositions
		c := report.Coverage
		summary.Sectors += c.TotalSectors
		summary.ValidPassages += c.ValidPassages
		summary.IncompleteMeasurements += c.IncompleteMeasurements
		summary.BoundaryCensoredMeasurements += c.BoundaryCensoredMeasurements
		summary.SectorsWithValidPassages += c.SectorsWithValidPassages
		summary.SectorsWithMatchedGPS += c.SectorsWithMatchedGPS
		summary.SectorsWithEvidenceButNoValidPassages += c.SectorsWithEvidenceButNoValidPassages
		summary.SectorsWithPassagesOnMultipleDates += c.SectorsWithPassagesOnMultipleDates
		summary.SectorHourPairsWithPassagesOnMultipleDates += c.SectorHourPairsWithPassagesOnMultipleDates
		summary.CellsWithValidPassages += c.CellsWithValidPassages
	}

	return summary, nil
}
