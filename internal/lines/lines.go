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
	// Use a temporary jurisdiction to validate stored geometry before exporting it.
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
