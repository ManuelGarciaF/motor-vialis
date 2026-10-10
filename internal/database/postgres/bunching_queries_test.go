package postgres

import (
	"strings"
	"testing"
)

func normalizeBunchingSQL(sql string) string {
	return strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(sql), ";")), " ")
}

func TestBunchingHistoryQueriesUseSameFilters(t *testing.T) {
	// Full, standalone statements intentionally duplicate the WHERE clause so
	// each SQL file remains readable/executable without template expansion.
	// Keep count and pagination eligibility from drifting apart.
	filter := func(sql string) string {
		t.Helper()
		start := strings.LastIndex(sql, "\nWHERE ")
		if start < 0 {
			t.Fatal("missing outer history filter")
		}
		clause := sql[start:]
		if end := strings.Index(clause, "\nORDER BY "); end >= 0 {
			clause = clause[:end]
		}
		return normalizeBunchingSQL(clause)
	}
	if filter(countBunchingHistorySQL) != filter(findBunchingHistorySQL) {
		t.Fatal("history total and page use different eligibility filters")
	}
}

func TestBunchingQueriesUseSameEventProjection(t *testing.T) {
	// scanEvent uses positional scanning. These queries must expose exactly the
	// same event columns/order, with history adding current_line_id at the end.
	projection := func(sql string) string {
		t.Helper()
		start := strings.Index(sql, "\nSELECT\n")
		end := strings.Index(sql, "\nFROM vialis.aglutinaciones_colectivos")
		if start < 0 || end <= start {
			t.Fatal("missing event projection")
		}
		columns := sql[start+len("\nSELECT\n") : end]
		if extra := strings.Index(columns, "\n    (\n        SELECT r.id_recorrido"); extra >= 0 {
			columns = strings.TrimSuffix(strings.TrimSpace(columns[:extra]), ",")
		}
		return normalizeBunchingSQL(columns)
	}
	expected := projection(findOpenBunchingSQL)
	for name, sql := range map[string]string{
		"current":       findCurrentBunchingSQL,
		"history":       findBunchingHistorySQL,
		"recent closed": findRecentClosedBunchingSQL,
	} {
		t.Run(name, func(t *testing.T) {
			if projection(sql) != expected {
				t.Fatal("event projection differs from scanEvent's shared order")
			}
		})
	}
}
