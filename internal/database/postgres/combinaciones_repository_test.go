package postgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/combinaciones"
)

// combinationRow builds one row of find_frequent_transfers.sql. The total and
// the maximum are window functions, so the query repeats them on every row.
func combinationRow(
	total int,
	maximum float64,
	firstID int64, firstLine string,
	secondID int64, secondLine string,
	estimated, alternatives float64,
	peakHour int,
	flowsJSON string,
) []any {
	return []any{
		total, maximum,
		firstID, firstLine, "TRONCAL", firstLine, 0,
		secondID, secondLine, "TRONCAL", secondLine, 1,
		estimated, alternatives, peakHour,
		"Rivadavia y Medrano", "Medrano 1200", 40,
		-58.42, -34.60,
		flowsJSON,
	}
}

const unFlujo = `[{"h3Origen":"88a","lonOrigen":-58.45,"latOrigen":-34.65,` +
	`"h3Destino":"88b","lonDestino":-58.37,"latDestino":-34.62,` +
	`"hora":9,"viajes":1900.5,"alternativas":3,` +
	`"nombreOrigen":"AV. RIVADAVIA 1200","nombreDestino":"CALLE 1149"}]`

func testRankingQuery() combinaciones.Query {
	return combinaciones.Query{Limit: 10, Offset: 0}
}

func TestCombinacionesRepositoryReadsARankedPage(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{values: [][]any{
		combinationRow(873, 9100, 1, "132", 2, "45", 4300, 2.5, 9, unFlujo),
		combinationRow(873, 9100, 3, "28", 4, "70", 3900, 11, 18, "[]"),
	}}}
	repository := newCombinacionesRepository(query.execute)

	found, total, maximum, err := repository.FindRanking(
		context.Background(),
		testRankingQuery(),
	)
	if err != nil {
		t.Fatalf("FindRanking() error = %v", err)
	}

	if len(found) != 2 {
		t.Fatalf("combinations = %d, want 2", len(found))
	}
	if total != 873 {
		t.Errorf("total = %d, want 873", total)
	}
	if maximum != 9100 {
		t.Errorf("maximum = %v, want 9100", maximum)
	}
	if found[0].First.Line != "132" || found[0].Second.Line != "45" {
		t.Errorf("first combination = %#v / %#v", found[0].First, found[0].Second)
	}
	if found[0].EstimatedTrips != 4300 || found[0].AverageAlternatives != 2.5 {
		t.Errorf("first combination estimate = %#v", found[0])
	}
	if found[0].PeakHour != 9 {
		t.Errorf("peak hour = %d, want 9", found[0].PeakHour)
	}
	if found[0].Transfer.WalkMeters != 40 ||
		found[0].Transfer.AlightingStopName == "" ||
		found[0].Transfer.BoardingStopName == "" {
		t.Errorf("transfer = %#v", found[0].Transfer)
	}
}

func TestCombinacionesRepositoryDecodesTheTopFlows(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{values: [][]any{
		combinationRow(1, 4300, 1, "132", 2, "45", 4300, 3, 9, unFlujo),
	}}}
	repository := newCombinacionesRepository(query.execute)

	found, _, _, err := repository.FindRanking(
		context.Background(),
		testRankingQuery(),
	)
	if err != nil {
		t.Fatalf("FindRanking() error = %v", err)
	}

	if len(found[0].TopFlows) != 1 {
		t.Fatalf("top flows = %#v, want 1", found[0].TopFlows)
	}
	flow := found[0].TopFlows[0]
	if flow.Origin.H3Index != "88a" || flow.Destination.H3Index != "88b" {
		t.Errorf("flow cells = %#v", flow)
	}
	if flow.Origin.Longitude != -58.45 || flow.Destination.Latitude != -34.62 {
		t.Errorf("flow coordinates = %#v", flow)
	}
	if flow.Hour != 9 || flow.EstimatedTrips != 1900.5 || flow.Alternatives != 3 {
		t.Errorf("flow numbers = %#v", flow)
	}
	// Sin los nombres, dos flujos con el mismo volumen y la misma hora son
	// indistinguibles en pantalla.
	if flow.Origin.Name != "AV. RIVADAVIA 1200" || flow.Destination.Name != "CALLE 1149" {
		t.Errorf("flow names = %q -> %q", flow.Origin.Name, flow.Destination.Name)
	}
}

// A combination with no stored flows is legitimate, and an empty JSON array is
// how the query says so.
func TestCombinacionesRepositoryAcceptsACombinationWithoutFlows(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{values: [][]any{
		combinationRow(1, 100, 1, "132", 2, "45", 100, 4, 7, "[]"),
	}}}
	repository := newCombinacionesRepository(query.execute)

	found, _, _, err := repository.FindRanking(
		context.Background(),
		testRankingQuery(),
	)
	if err != nil {
		t.Fatalf("FindRanking() error = %v", err)
	}
	if len(found[0].TopFlows) != 0 {
		t.Errorf("top flows = %#v, want none", found[0].TopFlows)
	}
}

// A page past the end of the ranking has no row to read the window functions
// from, so it reports zero rather than inventing a total.
func TestCombinacionesRepositoryReportsAnEmptyWindowAsZero(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{}}
	repository := newCombinacionesRepository(query.execute)

	found, total, maximum, err := repository.FindRanking(
		context.Background(),
		combinaciones.Query{Limit: 10, Offset: 9000},
	)
	if err != nil {
		t.Fatalf("FindRanking() error = %v", err)
	}
	if len(found) != 0 || total != 0 || maximum != 0 {
		t.Errorf("found = %#v, total = %d, maximum = %v", found, total, maximum)
	}
}

func TestCombinacionesRepositoryPassesTheQueryAsArguments(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{}}
	repository := newCombinacionesRepository(query.execute)
	nueve := 9

	if _, _, _, err := repository.FindRanking(
		context.Background(),
		combinaciones.Query{Hour: &nueve, Limit: 25, Offset: 50},
	); err != nil {
		t.Fatalf("FindRanking() error = %v", err)
	}

	// The order matters: the query reads them as $1..$3. The hour travels as a
	// pointer so a nil one reaches PostgreSQL as NULL, which is the whole-day
	// row and not a missing filter.
	wantArguments := []any{&nueve, 25, 50}
	if !reflect.DeepEqual(query.arguments, wantArguments) {
		t.Fatalf("arguments = %#v, want %#v", query.arguments, wantArguments)
	}
}

func TestCombinacionesRepositoryReportsAReadFailure(t *testing.T) {
	failure := errors.New("connection reset")
	query := &fakeQuery{rows: &fakeRows{
		values: [][]any{combinationRow(1, 1, 1, "a", 2, "b", 1, 1, 0, "[]")},
		err:    failure,
	}}
	repository := newCombinacionesRepository(query.execute)

	_, _, _, err := repository.FindRanking(context.Background(), testRankingQuery())

	if !errors.Is(err, failure) {
		t.Errorf("error = %v, want it to wrap %v", err, failure)
	}
}

func TestCombinacionesRepositoryReportsMalformedFlows(t *testing.T) {
	query := &fakeQuery{rows: &fakeRows{values: [][]any{
		combinationRow(1, 1, 1, "a", 2, "b", 1, 1, 0, "{no es json"),
	}}}
	repository := newCombinacionesRepository(query.execute)

	if _, _, _, err := repository.FindRanking(
		context.Background(),
		testRankingQuery(),
	); err == nil {
		t.Error("malformed JSON was accepted, want an error")
	}
}

// The ranking, the window and the whole-day semantics belong in SQL: applying
// them in Go would apply them to whatever the database happened to return.
func TestFindFrequentTransfersSQLKeepsTheSelectionInTheDatabase(t *testing.T) {
	required := []string{
		"vialis.combinaciones_lineas",
		"vialis.combinaciones_lineas_flujos",
		"nombre_origen",
		"nombre_destino",
		"vialis.conexiones_recorridos",
		"IS NOT DISTINCT FROM $1",
		"COUNT(*) OVER ()",
		"LIMIT $2",
		"OFFSET $3",
	}
	for _, fragment := range required {
		if !strings.Contains(findFrequentTransfersSQL, fragment) {
			t.Errorf("find_frequent_transfers.sql does not contain %q", fragment)
		}
	}
}
