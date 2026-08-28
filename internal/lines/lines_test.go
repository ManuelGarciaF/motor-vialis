package lines_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func testPolicy() lines.Policy {
	return lines.Policy{
		AlignmentToleranceMeters: 250,
		DefaultPageSize:          50,
		MaximumPageSize:          200,
	}
}

type fakeRepository struct {
	summaries []lines.Summary
	total     int
	stored    lines.StoredLine
	err       error
	received  lines.Query
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
	// A caller decoding the response should get [] rather than null.
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

// GTFS records no tariff authority, and the engine never infers one from
// geometry, so the export must not invent a jurisdiction: the caller sets it.
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

// A circular line calls at its first stop again at the end. The repeat needs
// its own id, because route.Validate requires ids unique within a route.
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

func TestGetReportsAGapInStoredGeometry(t *testing.T) {
	stored := storedLine()
	stored.Stops[1].PathToNext = nil
	repository := &fakeRepository{stored: stored}
	service := lines.NewService(repository, testPolicy())

	_, err := service.Get(context.Background(), 7)
	var notSimulable *lines.NotSimulableError
	if !errors.As(err, &notSimulable) {
		t.Fatalf("error = %v, want *NotSimulableError", err)
	}
	if notSimulable.LineID != 7 {
		t.Fatalf("line id = %d, want 7", notSimulable.LineID)
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

// simulable adds the jurisdiction the caller is expected to choose, so a
// validation failure can only come from the geometry the export built.
func simulable(exported route.Route) route.Route {
	exported.Jurisdiction = route.JurisdictionCABA
	return exported
}

// storedLine mirrors an ETL export whose segment boundaries were projected onto
// the GTFS shape and therefore land near, but not on, each stop.
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
