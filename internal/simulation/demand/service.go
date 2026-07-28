package demand

import (
	"context"
	"fmt"
	"sort"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// Repository provides the spatial candidates and aggregated demand required by
// the demand estimator.
type Repository interface {
	FindCellCandidates(
		ctx context.Context,
		input route.Route,
		radiusMeters float64,
	) ([]CellCandidate, error)
	FindDemandByStopPair(
		ctx context.Context,
		cells []AssignedCell,
	) ([]StopPairDemand, error)
}

// Service estimates potential demand.
type Service struct {
	repository              Repository
	radiusMeters            float64
	accessibilityCalculator AccessibilityCalculator
}

// NewService creates a demand estimator.
func NewService(
	repository Repository,
	radiusMeters float64,
	accessibilityCalculator AccessibilityCalculator,
) *Service {
	return &Service{
		repository:              repository,
		radiusMeters:            radiusMeters,
		accessibilityCalculator: accessibilityCalculator,
	}
}

// Estimate calculates potential demand for one validated ordered route.
func (service *Service) Estimate(
	ctx context.Context,
	input route.Route,
) (Result, error) {
	candidates, err := service.repository.FindCellCandidates(
		ctx,
		input,
		service.radiusMeters,
	)
	if err != nil {
		return Result{}, fmt.Errorf("find cell candidates: %w", err)
	}

	assignedCells := assignCells(
		candidates,
		service.radiusMeters,
		service.accessibilityCalculator,
	)
	pairDemand, err := service.repository.FindDemandByStopPair(ctx, assignedCells)
	if err != nil {
		return Result{}, fmt.Errorf("find demand by stop pair: %w", err)
	}

	result := Result{ByStopPair: pairDemand}
	for _, pair := range pairDemand {
		result.GrossDemand += pair.GrossDemand
		result.PotentialDemand += pair.PotentialDemand
	}
	return result, nil
}

func assignCells(
	candidates []CellCandidate,
	radiusMeters float64,
	accessibilityCalculator AccessibilityCalculator,
) []AssignedCell {
	selected := make(map[CellID]CellCandidate, len(candidates))
	for _, candidate := range candidates {
		current, exists := selected[candidate.CellID]
		if !exists || candidatePrecedes(candidate, current) {
			selected[candidate.CellID] = candidate
		}
	}

	result := make([]AssignedCell, 0, len(selected))
	for _, candidate := range selected {
		result = append(result, AssignedCell{
			StopOrder: candidate.StopOrder,
			StopID:    candidate.StopID,
			CellID:    candidate.CellID,
			Accessibility: accessibilityCalculator.Calculate(
				candidate.DistanceMeters,
				radiusMeters,
			),
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].StopOrder != result[right].StopOrder {
			return result[left].StopOrder < result[right].StopOrder
		}
		if result[left].CellID != result[right].CellID {
			return result[left].CellID < result[right].CellID
		}
		return result[left].StopID < result[right].StopID
	})
	return result
}

func candidatePrecedes(candidate, current CellCandidate) bool {
	if candidate.DistanceMeters != current.DistanceMeters {
		return candidate.DistanceMeters < current.DistanceMeters
	}
	if candidate.StopOrder != current.StopOrder {
		return candidate.StopOrder < current.StopOrder
	}
	return candidate.StopID < current.StopID
}
