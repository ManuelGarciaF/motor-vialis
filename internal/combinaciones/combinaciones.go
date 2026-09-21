// Package combinaciones ranks the pairs of bus lines that people appear to be
// combining, and how many trips each pair is estimated to carry.
//
// What the SUBE survey records bounds what this package may claim. A trip is
// one row: an origin, a destination, an hour, and how many stages it took.
// There is no stage table, no transfer point, and no line identifier anywhere
// in it. So "this trip needed a transfer" is knowable — it took more than one
// stage — and "these two lines carried it" is not.
//
// The ranking bridges that gap with a stated assumption rather than a
// measurement: sql/viajes/combinaciones_lineas.sql splits every
// origin-destination flow equally among the combinations that could have
// served it. A flow of 4.000 trips with 4 feasible combinations contributes
// 1.000 to each. That is why AverageAlternatives travels beside every estimate
// and why WeakEvidence exists: a number that came from splitting among two
// candidates is a strong claim, and the same number split among thirty is a
// conjecture, and nothing else in the response distinguishes them.
package combinaciones

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// ErrHourOutOfRange reports a banda horaria outside the 0..23 the survey uses.
// The column is an hour of the day, so 24 is not an empty result, it is a
// question that cannot be asked.
var ErrHourOutOfRange = errors.New("hour must be between 0 and 23")

// HoursInDay bounds the rango_horario column of vialis.viajes.
const HoursInDay = 24

// Policy holds the tunable rules this package applies. As in lines.Policy, the
// package declares the knobs it owns and internal/config decides their values,
// so nothing here reads the environment.
//
// The access radius and the transfer walking radius are deliberately absent:
// both are applied by the ETL that builds the ranking, so by the time a request
// arrives they are already baked into the stored rows. Declaring them here
// would suggest a request could change them.
type Policy struct {
	// DefaultPageSize is how many combinations a page returns when the caller
	// does not ask for a size, and MaximumPageSize caps what it may ask for.
	DefaultPageSize int
	MaximumPageSize int
	// WeakEvidenceAlternatives is the average number of feasible combinations
	// above which an estimate stops being a claim and becomes a conjecture, so
	// the response says so. It is a model parameter, not a presentation
	// detail: deciding when the engine is willing to assert something belongs
	// in a reviewable commit, not in whichever client happens to render it.
	WeakEvidenceAlternatives float64
}

// Leg is one of the two buses of a combination.
type Leg struct {
	LineID      int64  `json:"lineId"`
	Line        string `json:"line"`
	Branch      string `json:"branch,omitempty"`
	PublicName  string `json:"publicName"`
	DirectionID int    `json:"directionId"`
}

// TransferStop is where a combination changes buses. When both lines call at
// the same physical stop, the two names are the same place and WalkMeters is 0.
type TransferStop struct {
	AlightingStopName string `json:"alightingStopName"`
	BoardingStopName  string `json:"boardingStopName"`
	WalkMeters        int    `json:"walkMeters"`
	// Longitude and Latitude locate the stop where the second bus is boarded.
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

// Cell is one H3 cell of resolution 8, reported with the point the trip survey
// found most concurred inside it. The boundary is not sent: a client that needs
// the hexagon can derive it from the index, and repeating seven coordinates per
// cell would dominate the response.
type Cell struct {
	H3Index string `json:"h3Index"`
	// Name is the stop nearest to the cell, from the GTFS catalogue. An H3
	// index and a pair of coordinates name nothing to a person: without this,
	// two flows that agree on volume and hour read as the same row repeated
	// when they are different places.
	Name string `json:"name"`
	// Longitude and Latitude are hexagonos_viajes.punto_maxima_concurrencia,
	// which is where people in that cell actually start or end trips, not the
	// geometric centre of the hexagon.
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

// Flow is one origin-destination movement attributed to a combination. It says
// where the demand behind a pair of lines actually comes from.
type Flow struct {
	Origin      Cell `json:"origin"`
	Destination Cell `json:"destination"`
	Hour        int  `json:"hour"`
	// EstimatedTrips is this flow's share after the split, not the flow's whole
	// volume: the rest went to the other feasible combinations.
	EstimatedTrips float64 `json:"estimatedTrips"`
	// Alternatives is how many combinations this flow was split among. One
	// means the network left no other way of making the trip.
	Alternatives int `json:"alternatives"`
}

// Combination is one ranked pair of lines.
type Combination struct {
	First  Leg `json:"first"`
	Second Leg `json:"second"`
	// EstimatedTrips is the attributed volume, summed over every flow this
	// combination could serve. It is an estimate under the split described in
	// the package comment, never a measurement.
	EstimatedTrips float64 `json:"estimatedTrips"`
	// AverageAlternatives is how many feasible combinations the flows behind
	// that number were split among, weighted by volume. What matters is not
	// how many flows contributed but how many people did.
	AverageAlternatives float64 `json:"averageAlternatives"`
	// WeakEvidence marks an estimate spread across so many candidates that the
	// pair should not be read as an assertion. The threshold is the policy's.
	WeakEvidence bool `json:"weakEvidence"`
	// PeakHour is the banda horaria in which this pair concentrates the most
	// trips.
	PeakHour int          `json:"peakHour"`
	Transfer TransferStop `json:"transfer"`
	// TopFlows are the largest origin-destination movements behind the
	// estimate, largest first, at most three.
	TopFlows []Flow `json:"topFlows"`
}

// Page is one window over the ranking.
//
// Unlike lines.Similarities and like lines.Page, it carries a total and an
// offset: the ranking is a table a client pages through, and the whole point of
// precomputing it is that counting it costs nothing.
type Page struct {
	Combinations []Combination `json:"combinations"`
	// Hour is the banda horaria this page describes, or nil for the whole day,
	// repeated so a cached or shared response says what it is about.
	Hour  *int `json:"hour,omitempty"`
	Total int  `json:"total"`
	Limit int  `json:"limit"`
	// MaximumEstimatedTrips is the largest estimate in the whole ranking, not
	// in this page. It is what a client measures severity against, and reading
	// it off the page would make the second page paint itself against its own
	// first row.
	MaximumEstimatedTrips float64 `json:"maximumEstimatedTrips"`
	Offset                int     `json:"offset"`
}

// Request is what a caller asks for. Hour nil means the whole day; Limit of
// zero means the policy's default.
type Request struct {
	Hour   *int
	Limit  int
	Offset int
}

// Query is what the repository needs, with the paging already clamped.
type Query struct {
	Hour   *int
	Limit  int
	Offset int
}

// Repository reads the precomputed ranking.
type Repository interface {
	// FindRanking returns one window over the ranking, the total number of
	// combinations matching the query, and the largest estimate in it.
	FindRanking(ctx context.Context, query Query) ([]Combination, int, float64, error)
}

// Service reads the ranking of frequent line combinations.
type Service struct {
	repository Repository
	policy     Policy
}

func NewService(repository Repository, policy Policy) *Service {
	return &Service{repository: repository, policy: policy}
}

// FindRanking returns one page of the combinations people appear to be making,
// largest estimate first.
func (service *Service) FindRanking(
	ctx context.Context,
	request Request,
) (Page, error) {
	if request.Hour != nil &&
		(*request.Hour < 0 || *request.Hour >= HoursInDay) {
		return Page{}, ErrHourOutOfRange
	}

	limit := service.pageSize(request.Limit)
	offset := request.Offset
	if offset < 0 {
		offset = 0
	}

	found, total, maximum, err := service.repository.FindRanking(ctx, Query{
		Hour:   request.Hour,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return Page{}, fmt.Errorf("find combination ranking: %w", err)
	}
	if found == nil {
		found = []Combination{}
	}
	// The repository already orders the rows, because the window has to be
	// taken from the ranked list and not from an arbitrary one. Restating the
	// identical rule here is a stable no-op on those rows and keeps the ranking
	// readable and testable in Go, exactly as lines.FindSimilar does.
	sortByEstimatedTrips(found)
	for index := range found {
		service.markEvidence(&found[index])
		if found[index].TopFlows == nil {
			found[index].TopFlows = []Flow{}
		}
	}

	return Page{
		Combinations:          found,
		Hour:                  request.Hour,
		Total:                 total,
		Limit:                 limit,
		MaximumEstimatedTrips: maximum,
		Offset:                offset,
	}, nil
}

func (service *Service) pageSize(requested int) int {
	maximum := service.policy.MaximumPageSize
	if requested <= 0 {
		requested = service.policy.DefaultPageSize
	}
	if maximum > 0 && requested > maximum {
		return maximum
	}
	return requested
}

// markEvidence decides whether an estimate is thin enough to warn about.
//
// The rule lives here rather than in a client so that every client agrees on
// when the engine is willing to assert something, and so that changing its
// mind is a change to the engine.
func (service *Service) markEvidence(combination *Combination) {
	threshold := service.policy.WeakEvidenceAlternatives
	combination.WeakEvidence = threshold > 0 &&
		combination.AverageAlternatives > threshold
}

// sortByEstimatedTrips ranks combinations by attributed volume, largest first.
//
// The tie-break walks the two line ids rather than stopping at equal volumes,
// so two runs of the same query return the same page. Ties are not exotic here:
// a flow split among many combinations gives all of them the same share.
func sortByEstimatedTrips(combinations []Combination) {
	sort.SliceStable(combinations, func(left, right int) bool {
		first, second := combinations[left], combinations[right]
		if first.EstimatedTrips != second.EstimatedTrips {
			return first.EstimatedTrips > second.EstimatedTrips
		}
		if first.First.LineID != second.First.LineID {
			return first.First.LineID < second.First.LineID
		}
		return first.Second.LineID < second.Second.LineID
	})
}
