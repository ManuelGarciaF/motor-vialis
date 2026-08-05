package revenue

import (
	"context"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

// Repository reads the tariff bands applicable to a selected jurisdiction.
type Repository interface {
	FindTariffBands(context.Context, route.Jurisdiction) ([]TariffBand, error)
}

// Policy contains the assumptions that turn accessible demand into revenue.
type Policy struct {
	CaptureFactor       float64
	RegisteredCardShare float64
}

// Service calculates revenue from demand, route segment lengths, and tariffs.
type Service struct {
	repository Repository
	policy     Policy
}

func NewService(repository Repository, policy Policy) *Service {
	return &Service{repository: repository, policy: policy}
}

// Estimate calculates potential revenue for one validated route.
func (service *Service) Estimate(
	ctx context.Context,
	input route.Route,
	demandResult demand.Result,
	travelTimeResult traveltime.Result,
) (Result, error) {
	if err := validatePolicy(service.policy); err != nil {
		return Result{}, err
	}
	bands, err := service.repository.FindTariffBands(ctx, input.Jurisdiction)
	if err != nil {
		return Result{}, fmt.Errorf("find tariff bands: %w", err)
	}
	if err := validateBands(bands); err != nil {
		return Result{}, err
	}

	result := Result{
		Jurisdiction:        input.Jurisdiction,
		CaptureFactor:       service.policy.CaptureFactor,
		RegisteredCardShare: service.policy.RegisteredCardShare,
		ByStopPair:          make([]StopPairResult, 0, len(demandResult.ByStopPair)),
	}
	for _, pair := range demandResult.ByStopPair {
		distance, err := pairDistance(pair, travelTimeResult.BySegment)
		if err != nil {
			return Result{}, err
		}
		band, err := tariffForDistance(distance, bands)
		if err != nil {
			return Result{}, err
		}
		capturedDemand := pair.PotentialDemand * service.policy.CaptureFactor
		weightedFare := float64(band.RegisteredFareCents)*service.policy.RegisteredCardShare +
			float64(band.UnregisteredFareCents)*(1-service.policy.RegisteredCardShare)
		potentialRevenue := capturedDemand * weightedFare
		result.PotentialRevenueCents += potentialRevenue
		result.ByStopPair = append(result.ByStopPair, StopPairResult{
			OriginStopID: pair.OriginStopID, DestinationStopID: pair.DestinationStopID,
			DistanceMeters: distance, PotentialDemand: pair.PotentialDemand,
			CapturedDemand: capturedDemand, RegisteredFareCents: band.RegisteredFareCents,
			UnregisteredFareCents: band.UnregisteredFareCents,
			WeightedFareCents:     weightedFare, PotentialRevenueCents: potentialRevenue,
		})
	}
	return result, nil
}

func validatePolicy(policy Policy) error {
	if policy.CaptureFactor < 0 || policy.CaptureFactor > 1 {
		return fmt.Errorf("capture factor must be between 0 and 1")
	}
	if policy.RegisteredCardShare < 0 || policy.RegisteredCardShare > 1 {
		return fmt.Errorf("registered card share must be between 0 and 1")
	}
	return nil
}

func validateBands(bands []TariffBand) error {
	if len(bands) == 0 {
		return fmt.Errorf("no tariff bands configured")
	}
	for index, band := range bands {
		if band.MinimumDistanceMeters < 0 || band.RegisteredFareCents < 0 || band.UnregisteredFareCents < 0 ||
			(band.MaximumDistanceMeters != nil && *band.MaximumDistanceMeters <= band.MinimumDistanceMeters) {
			return fmt.Errorf("invalid tariff band %d", index)
		}
	}
	return nil
}

func pairDistance(pair demand.StopPairDemand, segments []traveltime.SegmentResult) (float64, error) {
	if pair.OriginStopOrder < 0 || pair.DestinationStopOrder <= pair.OriginStopOrder || pair.DestinationStopOrder > len(segments) {
		return 0, fmt.Errorf("invalid stop pair %q to %q", pair.OriginStopID, pair.DestinationStopID)
	}
	var distance float64
	for index := pair.OriginStopOrder; index < pair.DestinationStopOrder; index++ {
		segment := segments[index]
		if (index == pair.OriginStopOrder && segment.OriginStopID != pair.OriginStopID) ||
			(index == pair.DestinationStopOrder-1 && segment.DestinationStopID != pair.DestinationStopID) {
			return 0, fmt.Errorf("stop pair %q to %q does not match route segments", pair.OriginStopID, pair.DestinationStopID)
		}
		distance += segment.DistanceMeters
	}
	return distance, nil
}

func tariffForDistance(distance float64, bands []TariffBand) (TariffBand, error) {
	for _, band := range bands {
		if distance >= float64(band.MinimumDistanceMeters) &&
			(band.MaximumDistanceMeters == nil || distance < float64(*band.MaximumDistanceMeters)) {
			return band, nil
		}
	}
	return TariffBand{}, fmt.Errorf("no tariff band for %.2f meters", distance)
}
