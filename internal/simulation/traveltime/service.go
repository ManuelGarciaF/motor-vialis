package traveltime

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// Service estimates distance and commercial travel time by route segment.
type Service struct {
	repository Repository
	policy     Policy
}

// NewService creates a travel-time estimator.
func NewService(repository Repository, policy Policy) *Service {
	return &Service{repository: repository, policy: policy}
}

// Estimate measures and estimates one validated route.
func (service *Service) Estimate(
	ctx context.Context,
	input route.Route,
) (Result, error) {
	segments := segmentsFromRoute(input)
	measured, err := service.repository.FindSegmentReferences(
		ctx,
		segments,
		service.policy,
	)
	if err != nil {
		return Result{}, fmt.Errorf("find segment references: %w", err)
	}

	ordered, err := orderMeasuredSegments(measured, len(segments))
	if err != nil {
		return Result{}, err
	}

	result := Result{
		Confidence: ConfidenceHigh,
		BySegment:  make([]SegmentResult, 0, len(ordered)),
	}
	var global *GlobalPaces
	for _, segment := range ordered {
		paces, source, confidence, referenceCount, found :=
			service.selectLocalPaces(segment)
		if !found {
			if global == nil {
				fallback, fallbackErr := service.repository.FindGlobalPaces(
					ctx,
					service.policy,
				)
				if fallbackErr != nil {
					return Result{}, fmt.Errorf(
						"find global commercial paces: %w",
						fallbackErr,
					)
				}
				if !validPaces(fallback.Paces) || fallback.RouteCount <= 0 {
					return Result{}, fmt.Errorf(
						"find global commercial paces: no valid GTFS references",
					)
				}
				global = &fallback
			}
			paces = global.Paces
			source = SourceGlobal
			confidence = ConfidenceLow
			referenceCount = global.RouteCount
		}

		segmentResult := SegmentResult{
			OriginStopID:        segment.OriginStopID,
			DestinationStopID:   segment.DestinationStopID,
			DistanceMeters:      segment.LengthMeters,
			OffPeakSeconds:      estimatedSeconds(segment.LengthMeters, paces.OffPeak),
			TypicalSeconds:      estimatedSeconds(segment.LengthMeters, paces.Typical),
			PeakSeconds:         estimatedSeconds(segment.LengthMeters, paces.Peak),
			Confidence:          confidence,
			ReferenceRouteCount: referenceCount,
			Source:              source,
		}
		result.TotalDistanceMeters += segmentResult.DistanceMeters
		result.OffPeakSeconds += segmentResult.OffPeakSeconds
		result.TypicalSeconds += segmentResult.TypicalSeconds
		result.PeakSeconds += segmentResult.PeakSeconds
		result.Confidence = worseConfidence(result.Confidence, confidence)
		result.BySegment = append(result.BySegment, segmentResult)
	}
	return result, nil
}

func segmentsFromRoute(input route.Route) []Segment {
	segments := make([]Segment, 0, len(input.Stops)-1)
	for index := 0; index < len(input.Stops)-1; index++ {
		segments = append(segments, Segment{
			Order:             index,
			OriginStopID:      input.Stops[index].ID,
			DestinationStopID: input.Stops[index+1].ID,
			Path:              *input.Stops[index].PathToNext,
		})
	}
	return segments
}

func orderMeasuredSegments(
	measured []MeasuredSegment,
	count int,
) ([]MeasuredSegment, error) {
	if len(measured) != count {
		return nil, fmt.Errorf(
			"measure route segments: got %d segments, want %d",
			len(measured),
			count,
		)
	}
	ordered := make([]MeasuredSegment, count)
	seen := make([]bool, count)
	for _, segment := range measured {
		if segment.Order < 0 || segment.Order >= count || seen[segment.Order] {
			return nil, fmt.Errorf(
				"measure route segments: invalid segment order %d",
				segment.Order,
			)
		}
		if segment.LengthMeters <= 0 {
			return nil, fmt.Errorf(
				"measure route segments: segment %d has non-positive length",
				segment.Order,
			)
		}
		seen[segment.Order] = true
		ordered[segment.Order] = segment
	}
	return ordered, nil
}

func (service *Service) selectLocalPaces(
	segment MeasuredSegment,
) (Paces, ReferenceSource, Confidence, int, bool) {
	for radiusIndex, radius := range service.policy.ReferenceRadiiMeters {
		routes := consolidateRoutes(segment, radius)
		isLastRadius := radiusIndex == len(service.policy.ReferenceRadiiMeters)-1
		if len(routes) < service.policy.MinimumReferenceRoutes &&
			!(isLastRadius && len(routes) > 0) {
			continue
		}

		paces := Paces{
			OffPeak: weightedMedian(routes, func(value routePace) float64 {
				return value.Paces.OffPeak
			}),
			Typical: weightedMedian(routes, func(value routePace) float64 {
				return value.Paces.Typical
			}),
			Peak: weightedMedian(routes, func(value routePace) float64 {
				return value.Paces.Peak
			}),
		}
		if !validPaces(paces) {
			continue
		}
		return paces, sourceForRadius(radius), confidenceFor(radius, len(routes)),
			len(routes), true
	}
	return Paces{}, "", "", 0, false
}

type routePace struct {
	RouteID int64
	Paces   Paces
	Weight  float64
}

type paceAccumulator struct {
	weightedOffPeak float64
	weightedTypical float64
	weightedPeak    float64
	weight          float64
}

func consolidateRoutes(
	segment MeasuredSegment,
	radiusMeters float64,
) []routePace {
	accumulators := make(map[int64]paceAccumulator)
	for _, reference := range segment.References {
		if reference.RadiusMeters != radiusMeters ||
			!validPaces(reference.Paces) ||
			reference.OverlapMeters <= 0 ||
			reference.DistanceMeters < 0 {
			continue
		}
		overlapRatio := math.Min(
			1,
			reference.OverlapMeters/segment.LengthMeters,
		)
		proximityWeight := 1 / (1 + reference.DistanceMeters/radiusMeters)
		weight := overlapRatio * proximityWeight
		if weight <= 0 {
			continue
		}

		current := accumulators[reference.RouteID]
		current.weightedOffPeak += reference.Paces.OffPeak * weight
		current.weightedTypical += reference.Paces.Typical * weight
		current.weightedPeak += reference.Paces.Peak * weight
		current.weight += weight
		accumulators[reference.RouteID] = current
	}

	result := make([]routePace, 0, len(accumulators))
	for routeID, accumulator := range accumulators {
		if accumulator.weight <= 0 {
			continue
		}
		result = append(result, routePace{
			RouteID: routeID,
			Paces: Paces{
				OffPeak: accumulator.weightedOffPeak / accumulator.weight,
				Typical: accumulator.weightedTypical / accumulator.weight,
				Peak:    accumulator.weightedPeak / accumulator.weight,
			},
			Weight: math.Min(accumulator.weight, 1),
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].RouteID < result[right].RouteID
	})
	return result
}

func weightedMedian(
	values []routePace,
	value func(routePace) float64,
) float64 {
	ordered := append([]routePace(nil), values...)
	sort.SliceStable(ordered, func(left, right int) bool {
		leftValue := value(ordered[left])
		rightValue := value(ordered[right])
		if leftValue != rightValue {
			return leftValue < rightValue
		}
		return ordered[left].RouteID < ordered[right].RouteID
	})

	var totalWeight float64
	for _, item := range ordered {
		totalWeight += item.Weight
	}
	threshold := totalWeight / 2
	var cumulative float64
	for _, item := range ordered {
		cumulative += item.Weight
		if cumulative >= threshold {
			return value(item)
		}
	}
	return value(ordered[len(ordered)-1])
}

func validPaces(paces Paces) bool {
	return paces.OffPeak > 0 &&
		paces.OffPeak <= paces.Typical &&
		paces.Typical <= paces.Peak &&
		!math.IsNaN(paces.OffPeak) &&
		!math.IsNaN(paces.Typical) &&
		!math.IsNaN(paces.Peak) &&
		!math.IsInf(paces.OffPeak, 0) &&
		!math.IsInf(paces.Typical, 0) &&
		!math.IsInf(paces.Peak, 0)
}

func estimatedSeconds(distanceMeters, secondsPerMeter float64) int64 {
	return max(1, int64(math.Round(distanceMeters*secondsPerMeter)))
}

func sourceForRadius(radiusMeters float64) ReferenceSource {
	switch radiusMeters {
	case 100:
		return SourceLocal100
	case 300:
		return SourceLocal300
	default:
		return SourceLocal800
	}
}

func confidenceFor(radiusMeters float64, routeCount int) Confidence {
	if radiusMeters == 100 && routeCount >= 3 {
		return ConfidenceHigh
	}
	if routeCount >= 3 && radiusMeters <= 800 {
		return ConfidenceMedium
	}
	return ConfidenceLow
}

func worseConfidence(left, right Confidence) Confidence {
	rank := map[Confidence]int{
		ConfidenceHigh:   3,
		ConfidenceMedium: 2,
		ConfidenceLow:    1,
	}
	if rank[right] < rank[left] {
		return right
	}
	return left
}
