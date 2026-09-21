package lines

import (
	"context"
	"fmt"
	"sort"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// SimilarityRequest is a route someone drew, asked about against the stored
// lines.
//
// Limit of zero means the policy's default result count. It is the caller's
// ask, not the applied one: Similarities reports what was actually used.
type SimilarityRequest struct {
	Route route.Route
	Limit int
}

// SimilarityQuery is what the repository needs to find a corridor, which is
// less than the route the caller drew.
//
// The route is reduced to one continuous path plus its two endpoints before it
// reaches the database, because corridor overlap is a property of the drawn
// line, not of where its stops happen to fall. Doing the reduction in Go keeps
// it in one tested place instead of repeating a fold over stops in SQL.
type SimilarityQuery struct {
	// Path is the whole drawn route as a single LineString, as DrawnPath
	// builds it.
	Path route.LineString
	// Origin and Destination are the first and last stop of the drawn route.
	// They answer a different question from coverage — "does this line start
	// and end near mine?" — so they travel separately from the path.
	Origin      route.Position
	Destination route.Position
	// Limit is already clamped to the policy; the repository applies it as-is.
	Limit int
	// CorridorToleranceMeters is how far from one line the other may run and
	// still count as the same corridor.
	CorridorToleranceMeters float64
	// MinimumCoverage is the smallest coverage, in either direction, that makes
	// a candidate worth reporting.
	MinimumCoverage float64
}

// StoredMetrics carries the measurements the GTFS pipeline recorded for a
// stored line, as they are stored.
//
// Every field but the distance is a pointer because the corresponding column
// is nullable and mostly unpopulated: the feed gives a trip duration, while
// ridership and revenue come from data the project does not have for every
// line. A missing measurement is not a measurement of zero — reporting an
// absent passenger count as 0 would let a caller rank a line as empty when
// nobody ever counted it — so absence is preserved all the way to the JSON,
// where these fields are simply not present.
type StoredMetrics struct {
	// DistanceMeters repeats Summary.DistanceMeters so the measured record of
	// a line reads as one block. It is the only one of the four the DDL makes
	// NOT NULL.
	DistanceMeters int `json:"distanceMeters"`
	// TotalMinutes is the line's total scheduled running time
	// (recorridos.tiempo_total_minutos).
	TotalMinutes *int `json:"totalMinutes,omitempty"`
	// PassengerFlow is the recorded ridership of the line
	// (recorridos.caudal_pasajeros). The pipeline does not document a period,
	// so it is passed through without being turned into a rate.
	PassengerFlow *int `json:"passengerFlow,omitempty"`
	// Revenue is recorridos.ingreso_economico passed through unchanged. It is
	// deliberately not named Cents: the tariff table states its unit
	// explicitly (tarifa_registrada_centavos), this column does not, and no
	// loader in this repository writes it. Naming a unit the schema never
	// promised would be an invention, so the name says only what the column
	// means.
	Revenue *int64 `json:"revenue,omitempty"`
}

// Similarity is one stored line that shares a corridor with the drawn route.
//
// The two coverages are reported separately and are never averaged into a
// single score. They answer different questions: coverageOfProposed is "how
// much of what I drew is already served", coverageOfStored is "how much of
// that line is what I drew". A 3 km route lying entirely along a 40 km line
// scores 1.00 and 0.07 — that pair says "yours is a fragment of that line",
// which is not the same finding as two lines that cover each other 0.9 and
// 0.9. One averaged number reports 0.53 for the first case and 0.9 for the
// second, and in doing so destroys precisely the distinction the caller came
// for.
type Similarity struct {
	Line Summary `json:"line"`
	// CoverageOfProposed is the share of the drawn route's length that runs
	// within the corridor tolerance of the stored line, from 0 to 1.
	CoverageOfProposed float64 `json:"coverageOfProposed"`
	// CoverageOfStored is the share of the stored line's length that runs
	// within the corridor tolerance of the drawn route, from 0 to 1.
	CoverageOfStored float64 `json:"coverageOfStored"`
	// OriginDistanceMeters separates the drawn route's first stop from the
	// stored line's first stop, and DestinationDistanceMeters does the same for
	// the last ones. They are secondary signals: two lines may share a corridor
	// for most of their length and still begin in different neighbourhoods.
	OriginDistanceMeters      float64       `json:"originDistanceMeters"`
	DestinationDistanceMeters float64       `json:"destinationDistanceMeters"`
	Metrics                   StoredMetrics `json:"metrics"`
}

// Similarities is one answer to a corridor search.
//
// There is no total and no offset, unlike Page: a corridor search is a ranked
// shortlist, not a window a client pages through. Asking for more results
// widens the same list rather than moving along it.
type Similarities struct {
	Lines []Similarity `json:"lines"`
	// Limit is the result count that was applied, already clamped to the
	// policy maximum, so a caller can tell a short list from a truncated one.
	Limit int `json:"limit"`
}

// FindSimilar returns the stored lines that run along the same corridor as the
// route the caller drew, best match first.
//
// Nothing is simulated. The question "which existing lines does this overlap"
// is geometric, and answering it with demand, travel time, and revenue would
// cost three estimator passes per candidate to produce numbers the caller did
// not ask for. Whoever wants them runs /comparisons afterwards on the one or
// two lines this shortlist named.
func (service *Service) FindSimilar(
	ctx context.Context,
	request SimilarityRequest,
) (Similarities, error) {
	if err := validateDrawnRoute(request.Route); err != nil {
		return Similarities{}, err
	}

	limit := service.resultCount(request.Limit)
	stops := request.Route.Stops
	found, err := service.repository.FindSimilar(ctx, SimilarityQuery{
		Path:                    DrawnPath(request.Route),
		Origin:                  stops[0].Position,
		Destination:             stops[len(stops)-1].Position,
		Limit:                   limit,
		CorridorToleranceMeters: service.policy.SimilarityCorridorToleranceMeters,
		MinimumCoverage:         service.policy.SimilarityMinimumCoverage,
	})
	if err != nil {
		return Similarities{}, fmt.Errorf("find similar stored lines: %w", err)
	}
	if found == nil {
		found = []Similarity{}
	}
	// The repository already orders the rows, because the limit has to be
	// applied to the ranked list and not to an arbitrary one. Re-applying the
	// identical rule here is a stable no-op on those rows and buys two things:
	// the ranking is stated once in Go where it can be read and tested, and a
	// repository that returned rows in another order cannot quietly change
	// which lines a caller sees first.
	sortByCorridorAffinity(found)
	return Similarities{Lines: found, Limit: limit}, nil
}

func (service *Service) resultCount(requested int) int {
	maximum := service.policy.SimilarityMaximumResultCount
	if requested <= 0 {
		requested = service.policy.SimilarityDefaultResultCount
	}
	if maximum > 0 && requested > maximum {
		return maximum
	}
	return requested
}

// sortByCorridorAffinity ranks candidates by the weaker of their two
// coverages, descending.
//
// Sorting by the minimum is the conservative reading of the pair: a candidate
// is strongly similar only when it is similar in *both* directions, and the
// minimum is the only summary of the pair that cannot be raised by one
// direction alone. It is what keeps a fragment-of relationship (1.00 / 0.07)
// below a genuine twin (0.85 / 0.80), which sorting by the maximum, the sum,
// or the average all fail to do. The maximum then breaks ties, so that between
// two candidates the drawn route overlaps equally weakly, the one it overlaps
// more in the other direction comes first. The line id breaks what is left, so
// two runs of the same search return the same order.
func sortByCorridorAffinity(similarities []Similarity) {
	sort.SliceStable(similarities, func(left, right int) bool {
		first, second := similarities[left], similarities[right]
		leftWeakest := min(first.CoverageOfProposed, first.CoverageOfStored)
		rightWeakest := min(second.CoverageOfProposed, second.CoverageOfStored)
		if leftWeakest != rightWeakest {
			return leftWeakest > rightWeakest
		}
		leftStrongest := max(first.CoverageOfProposed, first.CoverageOfStored)
		rightStrongest := max(second.CoverageOfProposed, second.CoverageOfStored)
		if leftStrongest != rightStrongest {
			return leftStrongest > rightStrongest
		}
		return first.Line.ID < second.Line.ID
	})
}

// validateDrawnRoute checks only what a corridor search actually reads.
//
// It does not call route.Validate, which is the contract of a route about to
// be *simulated*, and demands two things similarity has no use for. It
// requires a jurisdiction, which selects a tariff table: no fare is computed
// here, so demanding one would force every caller to attach a meaningless
// value to get an answer about geometry. And it requires a pathToNext on every
// stop but the last, while a drawn route is allowed to leave one out —
// DrawnPath then joins the two stops with a straight segment, which is a
// reasonable guess at a corridor even though it would be an unacceptable
// description of an itinerary.
//
// What is left is what the search reads: at least two stops, so there is a
// corridor at all, and coordinates that describe a real place. The errors are
// route.ValidationError rooted at "route" like every other one, so the HTTP
// layer reports them exactly as it does for /simulations and /comparisons.
func validateDrawnRoute(input route.Route) error {
	if len(input.Stops) < 2 {
		return &route.ValidationError{
			Field:   "route.stops",
			Message: "must contain at least two stops",
		}
	}
	for index, stop := range input.Stops {
		field := fmt.Sprintf("route.stops[%d]", index)
		if err := validateDrawnPosition(field+".position", stop.Position); err != nil {
			return err
		}
		if stop.PathToNext == nil {
			continue
		}
		field += ".pathToNext"
		if index == len(input.Stops)-1 {
			return &route.ValidationError{
				Field:   field,
				Message: "must be omitted for the last stop",
			}
		}
		if len(stop.PathToNext.Positions) < 2 {
			return &route.ValidationError{
				Field:   field + ".coordinates",
				Message: "must contain at least two positions",
			}
		}
		for position, coordinate := range stop.PathToNext.Positions {
			if err := validateDrawnPosition(
				fmt.Sprintf("%s.coordinates[%d]", field, position),
				coordinate,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDrawnPosition(field string, position route.Position) error {
	if !finite(position.Latitude) ||
		position.Latitude < -90 || position.Latitude > 90 {
		return &route.ValidationError{
			Field:   field + ".latitude",
			Message: "must be between -90 and 90",
		}
	}
	if !finite(position.Longitude) ||
		position.Longitude < -180 || position.Longitude > 180 {
		return &route.ValidationError{
			Field:   field + ".longitude",
			Message: "must be between -180 and 180",
		}
	}
	return nil
}
