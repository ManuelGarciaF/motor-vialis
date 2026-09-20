package lines

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// ErrNotFound reports a line id that is not stored.
var ErrNotFound = errors.New("line not found")

// Policy holds the tunable rules this package applies. Like traveltime.Policy
// and revenue.Policy, the package declares the knobs it owns and internal/config
// decides their values, so nothing here reads the environment.
type Policy struct {
	// AlignmentToleranceMeters is the largest gap AlignStoredPathEndpoints
	// closes between a stored path endpoint and the stop it belongs to.
	AlignmentToleranceMeters float64
	// DefaultPageSize is how many lines a listing returns when the caller does
	// not ask for a size.
	DefaultPageSize int
	// MaximumPageSize caps what a caller may ask for, so one request cannot
	// pull the whole table.
	MaximumPageSize int
	// SimilarityCorridorToleranceMeters is how far a stored line may run from
	// the drawn route and still be counted as the same corridor.
	SimilarityCorridorToleranceMeters float64
	// SimilarityMinimumCoverage is the smallest coverage, in either direction,
	// that makes a candidate worth reporting at all.
	SimilarityMinimumCoverage float64
	// SimilarityDefaultResultCount is how many matches a corridor search
	// returns when the caller does not ask for a number.
	SimilarityDefaultResultCount int
	// SimilarityMaximumResultCount caps what a caller may ask for.
	SimilarityMaximumResultCount int
}

// Summary describes a stored line without any geometry. Listing every AMBA
// line with its shape would run to megabytes, so geometry is only ever sent by
// the detail lookup.
type Summary struct {
	ID             int64  `json:"id"`
	Line           string `json:"line"`
	Branch         string `json:"branch,omitempty"`
	PublicName     string `json:"publicName"`
	DirectionID    int    `json:"directionId"`
	Destination    string `json:"destination,omitempty"`
	Description    string `json:"description,omitempty"`
	DistanceMeters int    `json:"distanceMeters"`
	StopCount      int    `json:"stopCount"`
}

// StopDescription names one stop of a stored line.
//
// It is kept beside the route rather than inside it because route.Stop is the
// simulation input contract, and /simulations rejects unknown fields: adding a
// name there would make the exported route impossible to send back.
type StopDescription struct {
	StopOrder int    `json:"stopOrder"`
	StopID    string `json:"stopId"`
	Name      string `json:"name"`
	Code      string `json:"code,omitempty"`
}

// Detail is one stored line together with the route needed to simulate it.
type Detail struct {
	Line  Summary           `json:"line"`
	Route route.Route       `json:"route"`
	Stops []StopDescription `json:"stops"`
}

// Bounds is a geographic rectangle in WGS 84, used to list only the lines a
// map viewport can show.
type Bounds struct {
	MinLongitude float64
	MinLatitude  float64
	MaxLongitude float64
	MaxLatitude  float64
}

// Query selects and pages through the stored lines.
type Query struct {
	// Search matches the line, branch, public name, or destination.
	Search string
	// Bounds, when set, keeps only lines whose geometry reaches the rectangle.
	Bounds *Bounds
	// Limit of zero means the policy's default page size.
	Limit  int
	Offset int
}

// Page is one window over the stored lines. Total counts everything the query
// matched, not the window, so a caller can size its pager without walking it.
type Page struct {
	Lines  []Summary `json:"lines"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

// StoredStop is one call of a stored line, as it comes out of the database.
type StoredStop struct {
	// StopNumber is the GTFS stop_sequence, which orders the calls but is not
	// necessarily contiguous.
	StopNumber int
	GTFSStopID string
	Name       string
	Code       string
	Position   route.Position
	// PathToNext is nil on the last stop, and on any stop the ETL could not
	// project forward along the shape.
	PathToNext *route.LineString
}

// StoredLine is a whole line as the repository reads it, before it is turned
// into a simulable route.
type StoredLine struct {
	Summary Summary
	Stops   []StoredStop
}

// Repository reads the stored GTFS lines.
type Repository interface {
	// ListSummaries returns one page of matching lines and the total number of
	// matches.
	ListSummaries(ctx context.Context, query Query) ([]Summary, int, error)
	// FindLine returns one line with its stops, or ErrNotFound.
	FindLine(ctx context.Context, id int64) (StoredLine, error)
	// FindSimilar returns the stored lines sharing a corridor with the drawn
	// path, already filtered by the query's minimum coverage, ranked, and cut
	// to its limit. An empty result is not an error: drawing a route where no
	// line runs is a legitimate answer, and the one a proposal is looking for.
	FindSimilar(ctx context.Context, query SimilarityQuery) ([]Similarity, error)
}

// NotSimulableError reports a stored line whose geometry cannot produce a route
// the engine would accept. It is a fact about the stored data, not about the
// request, so it is reported apart from a validation error.
type NotSimulableError struct {
	LineID int64
	Reason string
}

func (err *NotSimulableError) Error() string {
	return fmt.Sprintf("line %d cannot be simulated: %s", err.LineID, err.Reason)
}

// Service exports stored lines as routes the simulation endpoints accept.
type Service struct {
	repository Repository
	policy     Policy
}

func NewService(repository Repository, policy Policy) *Service {
	return &Service{repository: repository, policy: policy}
}

// List returns one page of stored lines, clamping the requested window to the
// policy.
func (service *Service) List(ctx context.Context, query Query) (Page, error) {
	query.Search = strings.TrimSpace(query.Search)
	query.Limit = service.pageSize(query.Limit)
	if query.Offset < 0 {
		query.Offset = 0
	}

	summaries, total, err := service.repository.ListSummaries(ctx, query)
	if err != nil {
		return Page{}, fmt.Errorf("list stored lines: %w", err)
	}
	if summaries == nil {
		summaries = []Summary{}
	}
	return Page{
		Lines:  summaries,
		Total:  total,
		Limit:  query.Limit,
		Offset: query.Offset,
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

// Get returns one stored line as a route ready to be sent to /simulations.
//
// The exported route carries no jurisdiction. GTFS does not record which
// tariff authority applies to a line and the engine refuses to infer one from
// geometry, so choosing it belongs to whoever runs the simulation, not to a
// lookup of stored data.
func (service *Service) Get(ctx context.Context, id int64) (Detail, error) {
	stored, err := service.repository.FindLine(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detail{}, err
		}
		return Detail{}, fmt.Errorf("find stored line %d: %w", id, err)
	}
	if len(stored.Stops) < 2 {
		return Detail{}, &NotSimulableError{
			LineID: id,
			Reason: "it has fewer than two stored stops",
		}
	}

	stopIDs := uniqueStopIDs(stored.Stops)
	exported := route.Route{Stops: make([]route.Stop, len(stored.Stops))}
	descriptions := make([]StopDescription, len(stored.Stops))
	for index, stop := range stored.Stops {
		exported.Stops[index] = route.Stop{
			ID:       stopIDs[index],
			Position: stop.Position,
		}
		descriptions[index] = StopDescription{
			StopOrder: index,
			StopID:    stopIDs[index],
			Name:      stop.Name,
			Code:      stop.Code,
		}
		// The last stop carries no path, whatever the ETL stored for it.
		if index == len(stored.Stops)-1 {
			continue
		}
		if stop.PathToNext == nil {
			return Detail{}, &NotSimulableError{
				LineID: id,
				Reason: fmt.Sprintf(
					"no stored geometry between stops[%d] and stops[%d]",
					index,
					index+1,
				),
			}
		}
		// Copied because the alignment below rewrites the endpoints in place,
		// and what the repository read is not this function's to change.
		exported.Stops[index].PathToNext = &route.LineString{
			Positions: append(
				make([]route.Position, 0, len(stop.PathToNext.Positions)),
				stop.PathToNext.Positions...,
			),
		}
	}

	if err := AlignStoredPathEndpoints(
		&exported,
		service.policy.AlignmentToleranceMeters,
	); err != nil {
		return Detail{}, &NotSimulableError{LineID: id, Reason: err.Error()}
	}
	// Validating here keeps the promise the endpoint makes: what it returns can
	// be sent straight back to /simulations once a jurisdiction is added.
	// Without it a flaw in stored data would surface as a confusing 400 on a
	// route the caller never wrote.
	//
	// The probe borrows a valid jurisdiction so the check reaches the geometry
	// rules, which are the only ones this data can break. The exported route
	// keeps its empty jurisdiction.
	probe := exported
	probe.Jurisdiction = route.JurisdictionCABA
	if err := route.Validate(probe); err != nil {
		return Detail{}, &NotSimulableError{LineID: id, Reason: err.Error()}
	}

	summary := stored.Summary
	summary.ID = id
	summary.StopCount = len(stored.Stops)
	return Detail{Line: summary, Route: exported, Stops: descriptions}, nil
}

// uniqueStopIDs derives one route-unique id per call of the line.
//
// GTFS lets a line call at the same physical stop twice — a circular route ends
// where it began — while route.Validate requires ids unique within a route.
// The first call keeps the plain GTFS id so the common line stays recognisable;
// a repeat is suffixed with its stop_sequence, which is unique per line by the
// recorridos_paradas primary key.
func uniqueStopIDs(stops []StoredStop) []string {
	ids := make([]string, len(stops))
	seen := make(map[string]struct{}, len(stops))
	for index, stop := range stops {
		id := stop.GTFSStopID
		if _, exists := seen[id]; exists {
			id = fmt.Sprintf("%s#%d", stop.GTFSStopID, stop.StopNumber)
		}
		seen[id] = struct{}{}
		ids[index] = id
	}
	return ids
}
