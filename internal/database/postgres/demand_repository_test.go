package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestDemandRepositoryFindCellCandidates(t *testing.T) {
	query := &fakeQuery{
		rows: &fakeRows{values: [][]any{
			{0, "A", "88c2e311b1fffff", 0.0},
			{1, "B", "88c2e311b5fffff", 400.0},
		}},
	}
	repository := newDemandRepository(query.execute)
	input := route.Route{Stops: []route.Stop{
		{ID: "A", Position: route.Position{Latitude: -34.60, Longitude: -58.38}},
		{ID: "B", Position: route.Position{Latitude: -34.61, Longitude: -58.39}},
	}}

	actual, err := repository.FindCellCandidates(context.Background(), input, 800)
	if err != nil {
		t.Fatalf("FindCellCandidates() error = %v", err)
	}
	want := []demand.CellCandidate{
		{StopOrder: 0, StopID: "A", CellID: "88c2e311b1fffff", DistanceMeters: 0},
		{StopOrder: 1, StopID: "B", CellID: "88c2e311b5fffff", DistanceMeters: 400},
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("candidates = %#v, want %#v", actual, want)
	}
	if query.sql != findCellCandidatesSQL {
		t.Fatal("repository did not use the embedded cell-candidate query")
	}
	wantArguments := []any{
		[]string{"A", "B"},
		[]float64{-58.38, -58.39},
		[]float64{-34.60, -34.61},
		800.0,
	}
	if !reflect.DeepEqual(query.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", query.arguments, wantArguments)
	}
	if !strings.Contains(findCellCandidatesSQL, "distance_meters < $4") {
		t.Fatal("cell-candidate query must exclude cells at the configured radius")
	}
}

func TestDemandRepositoryFindDemandByStopPair(t *testing.T) {
	query := &fakeQuery{
		rows: &fakeRows{values: [][]any{
			{0, "A", 1, "B", 100.0, 70.0},
			{0, "A", 2, "C", 50.0, 30.0},
			{1, "B", 2, "C", 25.0, 18.0},
		}},
	}
	repository := newDemandRepository(query.execute)
	cells := []demand.AssignedCell{
		{StopOrder: 0, StopID: "A", CellID: "cell-a", Accessibility: 0.9},
		{StopOrder: 1, StopID: "B", CellID: "cell-b", Accessibility: 0.8},
		{StopOrder: 2, StopID: "C", CellID: "cell-c", Accessibility: 0.7},
	}

	actual, err := repository.FindDemandByStopPair(context.Background(), cells)
	if err != nil {
		t.Fatalf("FindDemandByStopPair() error = %v", err)
	}
	want := []demand.StopPairDemand{
		{OriginStopOrder: 0, OriginStopID: "A", DestinationStopOrder: 1, DestinationStopID: "B", GrossDemand: 100, PotentialDemand: 70},
		{OriginStopOrder: 0, OriginStopID: "A", DestinationStopOrder: 2, DestinationStopID: "C", GrossDemand: 50, PotentialDemand: 30},
		{OriginStopOrder: 1, OriginStopID: "B", DestinationStopOrder: 2, DestinationStopID: "C", GrossDemand: 25, PotentialDemand: 18},
	}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("demand = %#v, want %#v", actual, want)
	}
	if query.sql != findDemandByStopPairSQL {
		t.Fatal("repository did not use the embedded stop-pair query")
	}
	wantArguments := []any{
		[]int32{0, 1, 2},
		[]string{"A", "B", "C"},
		[]string{"cell-a", "cell-b", "cell-c"},
		[]float64{0.9, 0.8, 0.7},
	}
	if !reflect.DeepEqual(query.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", query.arguments, wantArguments)
	}
	if !strings.Contains(findDemandByStopPairSQL, "origin.stop_order < destination.stop_order") {
		t.Fatal("demand query must only include downstream stop pairs")
	}
}

func TestDemandRepositorySkipsDemandQueryWithoutAssignedCells(t *testing.T) {
	query := &fakeQuery{}
	repository := newDemandRepository(query.execute)

	actual, err := repository.FindDemandByStopPair(context.Background(), nil)
	if err != nil {
		t.Fatalf("FindDemandByStopPair() error = %v", err)
	}
	if len(actual) != 0 {
		t.Fatalf("demand = %#v, want empty", actual)
	}
	if query.calls != 0 {
		t.Fatal("database was queried without assigned cells")
	}
}

func TestDemandRepositoryPropagatesQueryError(t *testing.T) {
	wantError := errors.New("query failed")
	repository := newDemandRepository((&fakeQuery{err: wantError}).execute)

	_, err := repository.FindCellCandidates(context.Background(), route.Route{}, 800)
	if !errors.Is(err, wantError) {
		t.Fatalf("FindCellCandidates() error = %v, want wrapped query error", err)
	}
}

type fakeQuery struct {
	rows      rowIterator
	err       error
	calls     int
	sql       string
	arguments []any
}

func (query *fakeQuery) execute(
	_ context.Context,
	sql string,
	arguments ...any,
) (rowIterator, error) {
	query.calls++
	query.sql = sql
	query.arguments = arguments
	return query.rows, query.err
}

type fakeRows struct {
	values  [][]any
	current int
	err     error
}

func (rows *fakeRows) Next() bool {
	if rows.current >= len(rows.values) {
		return false
	}
	rows.current++
	return true
}

func (rows *fakeRows) Scan(destinations ...any) error {
	if rows.current == 0 || rows.current > len(rows.values) {
		return errors.New("Scan called without a current row")
	}
	values := rows.values[rows.current-1]
	if len(values) != len(destinations) {
		return fmt.Errorf("scan destinations = %d, values = %d", len(destinations), len(values))
	}
	for index, destination := range destinations {
		if err := assign(destination, values[index]); err != nil {
			return fmt.Errorf("column %d: %w", index, err)
		}
	}
	return nil
}

func (rows *fakeRows) Err() error { return rows.err }

func (rows *fakeRows) Close() {}

func assign(destination, value any) error {
	switch target := destination.(type) {
	case *int:
		*target = value.(int)
	case *int64:
		*target = value.(int64)
	case *string:
		*target = value.(string)
	case *float64:
		*target = value.(float64)
	case *[]byte:
		if value == nil {
			*target = nil
		} else {
			*target = []byte(value.(string))
		}
	case *demand.CellID:
		*target = demand.CellID(value.(string))
	case *sql.NullInt64:
		if value == nil {
			*target = sql.NullInt64{}
		} else {
			*target = sql.NullInt64{Int64: value.(int64), Valid: true}
		}
	case *sql.NullFloat64:
		if value == nil {
			*target = sql.NullFloat64{}
		} else {
			*target = sql.NullFloat64{Float64: value.(float64), Valid: true}
		}
	default:
		return fmt.Errorf("unsupported destination %T", destination)
	}
	return nil
}
