package simulation

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Repository provides the spatial candidates and aggregated demand required by
// the simulation.
type Repository interface {
	FindCellCandidates(ctx context.Context, route Route, radiusMeters float64) ([]CellCandidate, error)
	FindDemandByStopPair(ctx context.Context, cells []AssignedCell) ([]StopPairDemand, error)
}

// Service executes the potential-demand simulation.
type Service struct {
	repository              Repository
	radiusMeters            float64
	accessibilityCalculator AccessibilityCalculator
}

// NewService creates a simulation service.
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

// ValidationError reports an invalid route.
type ValidationError struct {
	Field   string
	Message string
}

func (err *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Message)
}

// Simulate calculates potential demand for one ordered route.
func (service *Service) Simulate(ctx context.Context, route Route) (Result, error) {
	if err := validateRoute(route); err != nil {
		return Result{}, err
	}

	candidates, err := service.repository.FindCellCandidates(ctx, route, service.radiusMeters)
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
	for _, demand := range pairDemand {
		result.GrossDemand += demand.GrossDemand
		result.PotentialDemand += demand.PotentialDemand
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

func validateRoute(route Route) error {
	if len(route.Stops) < 2 {
		return &ValidationError{Field: "route.stops", Message: "must contain at least two stops"}
	}

	stopIDs := make(map[string]struct{}, len(route.Stops))
	for index, stop := range route.Stops {
		field := fmt.Sprintf("route.stops[%d]", index)
		stopID := strings.TrimSpace(stop.ID)
		if stopID == "" {
			return &ValidationError{Field: field + ".id", Message: "must not be empty"}
		}
		if _, exists := stopIDs[stopID]; exists {
			return &ValidationError{Field: field + ".id", Message: "must be unique within the route"}
		}
		stopIDs[stopID] = struct{}{}

		if !finite(stop.Position.Latitude) || stop.Position.Latitude < -90 || stop.Position.Latitude > 90 {
			return &ValidationError{Field: field + ".position.latitude", Message: "must be between -90 and 90"}
		}
		if !finite(stop.Position.Longitude) || stop.Position.Longitude < -180 || stop.Position.Longitude > 180 {
			return &ValidationError{Field: field + ".position.longitude", Message: "must be between -180 and 180"}
		}
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
