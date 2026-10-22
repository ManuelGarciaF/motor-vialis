package postgres

import (
	"testing"

	scripts "github.com/ManuelGarciaF/vialis-motor/sql"
	"github.com/jackc/pgx/v5"
)

// Execute the entire bootstrap DDL, not only the persistence section used by
// focused fixtures. A rolled-back, isolated schema never touches production data.
func TestBottlenecksIncludedInCanonicalDDL(t *testing.T) {
	f := newBottlenecksStoreFixture(t)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	schema := f.store.schema + "_ddl"
	if _, err = tx.Exec(f.ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, queryInSchema(scripts.DDL, schema)); err != nil {
		t.Fatalf("canonical DDL: %v", err)
	}
	var tables int
	if err = tx.QueryRow(f.ctx, `
		SELECT count(*) FROM pg_tables
		WHERE schemaname=$1 AND starts_with(tablename, 'bottlenecks_')
	`, schema).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 5 {
		t.Fatalf("expected five operational tables, got %d", tables)
	}
	for _, table := range []string{
		"bottlenecks_modelos", "bottlenecks_trazados", "bottlenecks_sectores",
		"bottlenecks_mediciones", "bottlenecks_estados_productor",
	} {
		var count int
		if err = tx.QueryRow(f.ctx, "SELECT count(*) FROM "+pgx.Identifier{schema, table}.Sanitize()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("DDL populated %s", table)
		}
	}
}
