package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

func TestTravelTimeRepositoryFindSegmentReferences(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{values: [][]any{
		{
			0, "A", "B", 1200.0, 100.0,
			int64(10), 12.0, 1000.0, 0.10, 0.20, 0.30,
		},
		{
			0, "A", "B", 1200.0, 300.0,
			nil, nil, nil, nil, nil, nil,
		},
		{
			0, "A", "B", 1200.0, 800.0,
			int64(11), 50.0, 1200.0, 0.20, 0.30, 0.40,
		},
	}}}
	repository := newTravelTimeRepository(query.execute)
	path := route.LineString{Positions: []route.Position{
		{Latitude: -34.60, Longitude: -58.38},
		{Latitude: -34.61, Longitude: -58.39},
	}}
	policy := config.DefaultTravelTimePolicy()

	actual, err := repository.FindSegmentReferences(
		context.Background(),
		[]traveltime.Segment{{
			Order:             0,
			OriginStopID:      "A",
			DestinationStopID: "B",
			Path:              path,
		}},
		policy,
	)
	if err != nil {
		t.Fatalf("FindSegmentReferences() error = %v", err)
	}
	if len(actual) != 1 {
		t.Fatalf("segments = %d, want 1", len(actual))
	}
	if actual[0].LengthMeters != 1200 ||
		actual[0].OriginStopID != "A" ||
		actual[0].DestinationStopID != "B" {
		t.Fatalf("measured segment = %#v", actual[0])
	}
	if len(actual[0].References) != 2 {
		t.Fatalf("references = %#v, want 2", actual[0].References)
	}
	if actual[0].References[0].RouteID != 10 ||
		actual[0].References[1].RouteID != 11 {
		t.Fatalf("references = %#v", actual[0].References)
	}

	wantArguments := []any{
		[]int32{0},
		[]string{"A"},
		[]string{"B"},
		[]string{`{"type":"LineString","coordinates":[[-58.38,-34.6],[-58.39,-34.61]]}`},
		[]float64{100, 300, 800},
		60.0,
		2.0,
		80.0,
	}
	if !reflect.DeepEqual(query.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", query.arguments, wantArguments)
	}
	if !strings.Contains(findSegmentReferencesSQL, "ST_DWithin") ||
		!strings.Contains(findSegmentReferencesSQL, "ST_Azimuth") ||
		!strings.Contains(findSegmentReferencesSQL, "ST_Length") {
		t.Fatal("segment-reference query is missing spatial selection or measurement")
	}
}

func TestTravelTimeRepositoryKeepsSegmentWithoutReferences(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{values: [][]any{
		{0, "A", "B", 900.0, 100.0, nil, nil, nil, nil, nil, nil},
		{0, "A", "B", 900.0, 300.0, nil, nil, nil, nil, nil, nil},
		{0, "A", "B", 900.0, 800.0, nil, nil, nil, nil, nil, nil},
	}}}
	actual, err := newTravelTimeRepository(query.execute).FindSegmentReferences(
		context.Background(),
		[]traveltime.Segment{{
			Order: 0,
			Path: route.LineString{Positions: []route.Position{
				{},
				{Latitude: 0.01},
			}},
		}},
		config.DefaultTravelTimePolicy(),
	)
	if err != nil {
		t.Fatalf("FindSegmentReferences() error = %v", err)
	}
	if len(actual) != 1 || len(actual[0].References) != 0 {
		t.Fatalf("segments = %#v", actual)
	}
}

func TestTravelTimeRepositoryFindGlobalPaces(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{values: [][]any{
		{0.10, 0.20, 0.30, 25},
	}}}
	actual, err := newTravelTimeRepository(query.execute).FindGlobalPaces(
		context.Background(),
		config.DefaultTravelTimePolicy(),
	)
	if err != nil {
		t.Fatalf("FindGlobalPaces() error = %v", err)
	}
	want := traveltime.GlobalPaces{
		Paces:      traveltime.Paces{OffPeak: 0.10, Typical: 0.20, Peak: 0.30},
		RouteCount: 25,
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("paces = %#v, want %#v", actual, want)
	}
	if query.sql != findGlobalPacesSQL {
		t.Fatal("repository did not use the embedded global-pace query")
	}
	if !reflect.DeepEqual(query.arguments, []any{2.0, 80.0}) {
		t.Fatalf("arguments = %#v", query.arguments)
	}
}

func TestTravelTimeRepositoryReturnsEmptyGlobalPaces(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{values: [][]any{
		{nil, nil, nil, 0},
	}}}
	actual, err := newTravelTimeRepository(query.execute).FindGlobalPaces(
		context.Background(),
		config.DefaultTravelTimePolicy(),
	)
	if err != nil {
		t.Fatalf("FindGlobalPaces() error = %v", err)
	}
	if actual.RouteCount != 0 || actual.Paces != (traveltime.Paces{}) {
		t.Fatalf("paces = %#v, want empty", actual)
	}
}

func TestTravelTimeRepositoryWrapsQueryErrors(t *testing.T) {
	want := errors.New("query failed")
	repository := newTravelTimeRepository((&fakeQuery{err: want}).execute)
	_, err := repository.FindSegmentReferences(
		context.Background(),
		nil,
		config.DefaultTravelTimePolicy(),
	)
	if !errors.Is(err, want) {
		t.Fatalf("FindSegmentReferences() error = %v, want wrapped error", err)
	}
}
