package lines_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func testPolicy() lines.Policy {
	return lines.Policy{
		AlignmentToleranceMeters:          250,
		DefaultPageSize:                   50,
		MaximumPageSize:                   200,
		SimilarityCorridorToleranceMeters: 200,
		SimilarityMinimumCoverage:         0.20,
		SimilarityDefaultResultCount:      10,
		SimilarityMaximumResultCount:      50,
	}
}

type fakeRepository struct {
	summaries []lines.Summary
	total     int
	stored    lines.StoredLine
	similar   []lines.Similarity
	err       error
	received  lines.Query
	// receivedSimilarity records what the service asked the corridor search
	// for, which is the part of the drawn route it decided to send.
	receivedSimilarity lines.SimilarityQuery
}

func (repository *fakeRepository) ListSummaries(
	_ context.Context,
	query lines.Query,
) ([]lines.Summary, int, error) {
	repository.received = query
	return repository.summaries, repository.total, repository.err
}

func (repository *fakeRepository) FindLine(
	_ context.Context,
	_ int64,
) (lines.StoredLine, error) {
	return repository.stored, repository.err
}

func (repository *fakeRepository) FindSimilar(
	_ context.Context,
	query lines.SimilarityQuery,
) ([]lines.Similarity, error) {
	repository.receivedSimilarity = query
	return repository.similar, repository.err
}

func TestListAppliesTheDefaultPageSize(t *testing.T) {
	repository := &fakeRepository{total: 300}
	service := lines.NewService(repository, testPolicy())

	page, err := service.List(context.Background(), lines.Query{Search: "  132 "})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repository.received.Limit != 50 {
		t.Fatalf("limit = %d, want the policy default 50", repository.received.Limit)
	}
	if repository.received.Search != "132" {
		t.Fatalf("search = %q, want it trimmed to %q", repository.received.Search, "132")
	}
	if page.Total != 300 {
		t.Fatalf("total = %d, want 300", page.Total)
	}
	if page.Lines == nil {
		t.Fatal("lines = nil, want an empty slice")
	}
}

func TestListClampsThePageSizeToTheMaximum(t *testing.T) {
	repository := &fakeRepository{}
	service := lines.NewService(repository, testPolicy())

	page, err := service.List(context.Background(), lines.Query{Limit: 5000})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repository.received.Limit != 200 {
		t.Fatalf("limit = %d, want the policy maximum 200", repository.received.Limit)
	}
	if page.Limit != 200 {
		t.Fatalf("reported limit = %d, want the clamped 200", page.Limit)
	}
}

func TestGetExportsARouteTheEngineAccepts(t *testing.T) {
	repository := &fakeRepository{stored: storedLine()}
	service := lines.NewService(repository, testPolicy())

	detail, err := service.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if err := route.Validate(simulable(detail.Route)); err != nil {
		t.Fatalf("exported route validation error = %v", err)
	}
	if detail.Line.ID != 7 || detail.Line.StopCount != 3 {
		t.Fatalf("summary = %#v, want id 7 and 3 stops", detail.Line)
	}
	if len(detail.Stops) != len(detail.Route.Stops) {
		t.Fatalf(
			"stop descriptions = %d, want one per route stop (%d)",
			len(detail.Stops),
			len(detail.Route.Stops),
		)
	}
	for index, description := range detail.Stops {
		if description.StopOrder != index {
			t.Fatalf("stops[%d].stopOrder = %d", index, description.StopOrder)
		}
		if description.StopID != detail.Route.Stops[index].ID {
			t.Fatalf(
				"stops[%d].stopId = %q, want %q",
				index,
				description.StopID,
				detail.Route.Stops[index].ID,
			)
		}
	}
	if detail.Stops[0].Name != "Retiro" {
		t.Fatalf("stops[0].name = %q, want Retiro", detail.Stops[0].Name)
	}
}

// Stored GTFS lines must not invent a tariff jurisdiction.
func TestGetExportsNoJurisdiction(t *testing.T) {
	repository := &fakeRepository{stored: storedLine()}
	service := lines.NewService(repository, testPolicy())

	detail, err := service.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Route.Jurisdiction != "" {
		t.Fatalf("jurisdiction = %q, want it left empty", detail.Route.Jurisdiction)
	}
}

// Repeated stops in circular lines need route-unique IDs.
func TestGetDisambiguatesRepeatedStops(t *testing.T) {
	stored := storedLine()
	stored.Stops[2].GTFSStopID = stored.Stops[0].GTFSStopID
	repository := &fakeRepository{stored: stored}
	service := lines.NewService(repository, testPolicy())

	detail, err := service.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if err := route.Validate(simulable(detail.Route)); err != nil {
		t.Fatalf("exported route validation error = %v", err)
	}
	if detail.Route.Stops[0].ID != "2031665" {
		t.Fatalf(
			"first call id = %q, want the plain GTFS id",
			detail.Route.Stops[0].ID,
		)
	}
	if detail.Route.Stops[2].ID != "2031665#30" {
		t.Fatalf(
			"repeated call id = %q, want it suffixed with the stop sequence",
			detail.Route.Stops[2].ID,
		)
	}
}

func TestGetMarksACompleteLineAsSimulable(t *testing.T) {
	repository := &fakeRepository{stored: storedLine()}
	service := lines.NewService(repository, testPolicy())

	detail, err := service.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !detail.Simulable || detail.NotSimulableReason != "" {
		t.Fatalf(
			"simulable = %t, reason = %q, want a simulable line with no reason",
			detail.Simulable,
			detail.NotSimulableReason,
		)
	}
}

// A dropped segment must not hide the rest of the line: it is still exported
// for drawing, without inventing the missing geometry, and flagged instead.
func TestGetExportsALineWithAGapAsNotSimulable(t *testing.T) {
	stored := storedLine()
	stored.Stops[1].PathToNext = nil
	repository := &fakeRepository{stored: stored}
	service := lines.NewService(repository, testPolicy())

	detail, err := service.Get(context.Background(), 7)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if detail.Simulable {
		t.Fatal("simulable = true, want false")
	}
	want := "line 7 cannot be simulated: no stored geometry between stops[1] and stops[2]"
	if detail.NotSimulableReason != want {
		t.Fatalf("reason = %q, want %q", detail.NotSimulableReason, want)
	}
	if len(detail.Route.Stops) != 3 {
		t.Fatalf("stops = %d, want all 3", len(detail.Route.Stops))
	}
	if detail.Route.Stops[0].PathToNext == nil {
		t.Fatal("stops[0].pathToNext = nil, want the stored segment")
	}
	if detail.Route.Stops[1].PathToNext != nil {
		t.Fatal("stops[1].pathToNext was invented for a dropped segment")
	}
	// The exported route is exactly what POST /simulations must keep rejecting.
	if err := route.Validate(simulable(detail.Route)); err == nil {
		t.Fatal("exported route validation error = nil, want the gap rejected")
	}
}

// The segments a gapped line keeps are still checked like any stored segment.
func TestGetRejectsAGappedLineWithAnInvalidSegment(t *testing.T) {
	stored := storedLine()
	stored.Stops[0].PathToNext = nil
	positions := stored.Stops[1].PathToNext.Positions
	for left, right := 0, len(positions)-1; left < right; left, right = left+1, right-1 {
		positions[left], positions[right] = positions[right], positions[left]
	}
	repository := &fakeRepository{stored: stored}
	service := lines.NewService(repository, testPolicy())

	_, err := service.Get(context.Background(), 7)
	var notSimulable *lines.NotSimulableError
	if !errors.As(err, &notSimulable) {
		t.Fatalf("error = %v, want *NotSimulableError", err)
	}
	if !strings.Contains(notSimulable.Reason, "route.stops[1].pathToNext") {
		t.Fatalf("reason = %q, want it to name stops[1]", notSimulable.Reason)
	}
}

func TestGetRejectsALineWithFewerThanTwoStops(t *testing.T) {
	stored := storedLine()
	stored.Stops = stored.Stops[:1]
	repository := &fakeRepository{stored: stored}
	service := lines.NewService(repository, testPolicy())

	_, err := service.Get(context.Background(), 7)
	var notSimulable *lines.NotSimulableError
	if !errors.As(err, &notSimulable) {
		t.Fatalf("error = %v, want *NotSimulableError", err)
	}
}

func TestGetPassesThroughNotFound(t *testing.T) {
	repository := &fakeRepository{err: lines.ErrNotFound}
	service := lines.NewService(repository, testPolicy())

	_, err := service.Get(context.Background(), 7)
	if !errors.Is(err, lines.ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// simulable adds the caller-owned jurisdiction before validation.
func simulable(exported route.Route) route.Route {
	exported.Jurisdiction = route.JurisdictionCABA
	return exported
}

// storedLine has GTFS-projected endpoints near, but not on, each stop.
func storedLine() lines.StoredLine {
	return lines.StoredLine{
		Summary: lines.Summary{
			Line:           "132",
			Branch:         "A",
			PublicName:     "132 - Retiro",
			DirectionID:    0,
			DistanceMeters: 1200,
		},
		Stops: []lines.StoredStop{
			{
				StopNumber: 10,
				GTFSStopID: "2031665",
				Name:       "Retiro",
				Code:       "1665",
				Position:   route.Position{Latitude: -34.586005, Longitude: -58.373625},
				PathToNext: &route.LineString{Positions: []route.Position{
					{Latitude: -34.586005, Longitude: -58.373625},
					{Latitude: -34.589460, Longitude: -58.372723},
					{Latitude: -34.589648151, Longitude: -58.372870385},
				}},
			},
			{
				StopNumber: 20,
				GTFSStopID: "204232",
				Name:       "Callao",
				Position:   route.Position{Latitude: -34.589460, Longitude: -58.372723},
				PathToNext: &route.LineString{Positions: []route.Position{
					{Latitude: -34.589648151, Longitude: -58.372870385},
					{Latitude: -34.591970, Longitude: -58.374470},
					{Latitude: -34.592301286, Longitude: -58.374740954},
				}},
			},
			{
				StopNumber: 30,
				GTFSStopID: "204208",
				Name:       "Congreso",
				Position:   route.Position{Latitude: -34.591970, Longitude: -58.374470},
			},
		},
	}
}
