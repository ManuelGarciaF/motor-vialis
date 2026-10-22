package bottlenecks

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// DetectionPolicy is experimental: recurrence counts passages, not distinct days
// or vehicles. Measurement policy and the producer checkpoint remain unchanged.
type DetectionPolicy struct {
	SpeedBelowKPH       float64 `json:"speedBelowKph"`
	MinimumSlowPassages int     `json:"minimumSlowPassages"`
}

type DetectionBounds struct{ MinLongitude, MinLatitude, MaxLongitude, MaxLatitude float64 }
type DetectionQuery struct {
	From, To time.Time
	Line     string
	Bounds   *DetectionBounds
}

type DetectionQueryError struct{ Field, Message string }

func (e *DetectionQueryError) Error() string { return e.Field + ": " + e.Message }

func (q DetectionQuery) Validate() error {
	if q.From.IsZero() {
		return &DetectionQueryError{"from", "must be an RFC3339 timestamp"}
	}
	if q.To.IsZero() {
		return &DetectionQueryError{"to", "must be an RFC3339 timestamp"}
	}
	if !q.From.Before(q.To) {
		return &DetectionQueryError{"to", "must be after from"}
	}
	if b := q.Bounds; b != nil {
		for _, v := range []float64{b.MinLongitude, b.MinLatitude, b.MaxLongitude, b.MaxLatitude} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return &DetectionQueryError{"bbox", "must contain finite coordinates"}
			}
		}
		if b.MinLongitude < -180 || b.MaxLongitude > 180 || b.MinLatitude < -90 || b.MaxLatitude > 90 || b.MinLongitude > b.MaxLongitude || b.MinLatitude > b.MaxLatitude {
			return &DetectionQueryError{"bbox", "must be an ordered WGS84 rectangle"}
		}
	}
	return nil
}

type DetectedSector struct {
	LayoutID           int64            `json:"layoutId"`
	Sector             int              `json:"sector"`
	GTFSRouteID        string           `json:"gtfsRouteId"`
	ShapeHash          string           `json:"shapeHash"`
	Line               string           `json:"line"`
	Branch             string           `json:"branch"`
	Direction          int              `json:"direction"`
	Geometry           route.LineString `json:"geometry"`
	LengthMeters       float64          `json:"lengthMeters"`
	RouteEnd           bool             `json:"routeEnd"`
	CompletePassages   int64            `json:"completePassages"`
	IncompletePassages int64            `json:"incompletePassages"`
	SlowPassages       int64            `json:"slowPassages"`
	SlowPercentage     float64          `json:"slowPercentage"`
	MedianSpeedKPH     float64          `json:"medianSpeedKph"`
	LastEntryAt        time.Time        `json:"lastEntryAt"`
}
type DetectionData struct {
	Sectors []DetectedSector `json:"sectors"`
}
type DetectionPage struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	DetectionData
}
type DetectionRepository interface {
	FindDetectedSectors(context.Context, string, DetectionQuery, DetectionPolicy) (DetectionData, error)
}
type DetectionService struct {
	repository DetectionRepository
	model      string
	policy     DetectionPolicy
}

func NewDetectionService(repository DetectionRepository, model string, policy DetectionPolicy) (*DetectionService, error) {
	if repository == nil || strings.TrimSpace(model) == "" || policy.SpeedBelowKPH <= 0 || math.IsNaN(policy.SpeedBelowKPH) || math.IsInf(policy.SpeedBelowKPH, 0) || policy.MinimumSlowPassages < 1 {
		return nil, fmt.Errorf("invalid bottlenecks detection configuration")
	}
	return &DetectionService{repository, model, policy}, nil
}
func (s *DetectionService) List(ctx context.Context, q DetectionQuery) (DetectionPage, error) {
	if err := q.Validate(); err != nil {
		return DetectionPage{}, err
	}
	q.From, q.To = q.From.UTC(), q.To.UTC()
	q.Line = strings.TrimSpace(q.Line)
	data, err := s.repository.FindDetectedSectors(ctx, s.model, q, s.policy)
	if err != nil {
		return DetectionPage{}, err
	}
	if data.Sectors == nil {
		data.Sectors = []DetectedSector{}
	}
	return DetectionPage{From: q.From, To: q.To, DetectionData: data}, nil
}
