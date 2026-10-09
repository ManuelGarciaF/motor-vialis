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

// Policy holds the configurable rules for exporting stored lines.
type Policy struct {
	// AlignmentToleranceMeters is the largest endpoint gap that may be closed.
	AlignmentToleranceMeters float64
	// DefaultPageSize applies when the caller omits a limit.
	DefaultPageSize int
	// MaximumPageSize caps a single listing request.
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

// Summary describes a stored line without its geometry.
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

// StopDescription keeps display metadata outside the re-postable route contract.
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
	// Simulable is false when the ETL dropped some segment of the stored line:
	// the route still carries every stop and every segment that exists, so the
	// line can be drawn, but POST /simulations would reject it as it is.
	Simulable bool `json:"simulable"`
	// NotSimulableReason explains a false Simulable; it is empty otherwise.
	NotSimulableReason string `json:"notSimulableReason,omitempty"`
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

// Page is one result window; Total counts all matching lines.
type Page struct {
	Lines  []Summary `json:"lines"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

// StoredStop is one call of a stored line, as it comes out of the database.
type StoredStop struct {
	// StopNumber is the potentially non-contiguous GTFS stop_sequence.
	StopNumber int
	GTFSStopID string
	Name       string
	Code       string
	Position   route.Position
	// PathToNext is nil when no following segment is stored.
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

// NotSimulableError reports invalid stored geometry, not invalid caller input.
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

// Get returns a stored line as a route. The caller must supply its jurisdiction.
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
	var gaps []string
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
		if index == len(stored.Stops)-1 {
			continue
		}
		// A dropped segment still lets the line be drawn; it only stops it from
		// being simulated, so it is reported instead of failing the whole line.
		if stop.PathToNext == nil {
			gaps = append(gaps, fmt.Sprintf("stops[%d] and stops[%d]", index, index+1))
			continue
		}
		// Alignment mutates endpoints, so keep repository data unchanged.
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
	if err := validateStoredSegments(exported, len(gaps) > 0); err != nil {
		return Detail{}, &NotSimulableError{LineID: id, Reason: err.Error()}
	}

	summary := stored.Summary
	summary.ID = id
	summary.StopCount = len(stored.Stops)
	detail := Detail{
		Line:      summary,
		Route:     exported,
		Stops:     descriptions,
		Simulable: len(gaps) == 0,
	}
	if len(gaps) > 0 {
		detail.NotSimulableReason = (&NotSimulableError{
			LineID: id,
			Reason: "no stored geometry between " + strings.Join(gaps, ", "),
		}).Error()
	}
	return detail, nil
}

// validateStoredSegments checks stored geometry before exporting it, using a
// temporary jurisdiction. A complete route goes through the same validation as
// POST /simulations; a route with dropped segments cannot, so each segment it
// does have is validated on its own, and only the gaps are left unchecked.
func validateStoredSegments(exported route.Route, hasGaps bool) error {
	if !hasGaps {
		probe := exported
		probe.Jurisdiction = route.JurisdictionCABA
		return route.Validate(probe)
	}
	for index := 0; index < len(exported.Stops)-1; index++ {
		if exported.Stops[index].PathToNext == nil {
			continue
		}
		next := exported.Stops[index+1]
		next.PathToNext = nil
		probe := route.Route{
			Jurisdiction: route.JurisdictionCABA,
			Stops:        []route.Stop{exported.Stops[index], next},
		}
		if err := route.Validate(probe); err != nil {
			return rebaseSegmentError(err, index)
		}
	}
	return nil
}

// rebaseSegmentError renames a two-stop probe's fields to the stops they are
// in the whole route, so the reason points at the stored segment.
func rebaseSegmentError(err error, index int) error {
	var validationError *route.ValidationError
	if !errors.As(err, &validationError) {
		return err
	}
	field := validationError.Field
	for offset := range 2 {
		probeField := fmt.Sprintf("route.stops[%d]", offset)
		if rest, found := strings.CutPrefix(field, probeField); found {
			field = fmt.Sprintf("route.stops[%d]%s", index+offset, rest)
			break
		}
	}
	return &route.ValidationError{Field: field, Message: validationError.Message}
}

// uniqueStopIDs suffixes repeated GTFS stops with their unique stop_sequence.
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
