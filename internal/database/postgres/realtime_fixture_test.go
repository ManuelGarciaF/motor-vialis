package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Each integration test owns an outer transaction and an isolated schema.
// Cleanup rolls back before closing the pool and cancelling the test context,
// including when a fixture or assertion fails with Fatal.
type realtimeFixture struct {
	t      *testing.T
	ctx    context.Context
	tx     pgx.Tx
	schema string
}

func newRealtimeFixture(t *testing.T, timeout time.Duration) *realtimeFixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Rollback must work even if the test's deadline has expired.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()

		if err := tx.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback realtime fixture: %v", err)
		}
	})
	schema := fmt.Sprintf("realtime_test_%d", time.Now().UnixNano())
	if _, err := tx.Exec(ctx, `
		CREATE SCHEMA
	`+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	return &realtimeFixture{t: t, ctx: ctx, tx: tx, schema: schema}
}

func (f *realtimeFixture) sql(query string) string {
	return queryInSchema(query, f.schema)
}

// No migrations run implicitly: legacy fixtures build their old schema first,
// and idempotency tests can deliberately repeat a migration in the list.
func (f *realtimeFixture) migrate(files ...string) {
	f.t.Helper()
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join("../../../sql/recorridos", file))
		if err != nil {
			f.t.Fatal(err)
		}
		script := strings.NewReplacer("\\set ON_ERROR_STOP on", "", "BEGIN;", "", "COMMIT;", "").Replace(string(data))
		if _, err := f.tx.Exec(f.ctx, f.sql(script)); err != nil {
			f.t.Fatalf("apply %s: %v", file, err)
		}
	}
}

func (f *realtimeFixture) realtimeRepository() *RealtimeRepository {
	return &RealtimeRepository{database: f.tx, retentionDays: 7, schema: f.schema}
}

func (f *realtimeFixture) bunchingRepository() *BunchingRepository {
	return &BunchingRepository{database: f.tx, schema: f.schema}
}

func TestRealtimeFixtureIsolation(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var schemas []string
	var transactions []pgx.Tx
	t.Run("isolated schemas", func(t *testing.T) {
		first := newRealtimeFixture(t, time.Minute)
		second := newRealtimeFixture(t, time.Minute)
		if first.schema == second.schema {
			t.Fatal("fixtures share a schema")
		}
		schemas = []string{first.schema, second.schema}
		transactions = []pgx.Tx{first.tx, second.tx}
		first.migrate("migrar_realtime.sql", "migrar_realtime.sql")
		var captures int
		if err := first.tx.QueryRow(first.ctx, first.sql(`
			SELECT count(*)
			FROM vialis.capturas_colectivos
		`)).Scan(&captures); err != nil || captures != 0 {
			t.Fatalf("fresh migrated fixture: %d %v", captures, err)
		}
		var tables int
		if err := second.tx.QueryRow(second.ctx, `
			SELECT count(*)
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1
		`, second.schema).Scan(&tables); err != nil || tables != 0 {
			t.Fatalf("empty fixture applied migrations implicitly: %d %v", tables, err)
		}
	})
	// Subtest Cleanup has run: both outer transactions must be closed and
	// neither the migrated nor the empty schema may remain in the database.
	for _, tx := range transactions {
		if _, err := tx.Exec(ctx, `
			SELECT 1
		`); !errors.Is(err, pgx.ErrTxClosed) {
			t.Fatalf("fixture transaction not rolled back: %v", err)
		}
	}

	var retained int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_namespace
		WHERE nspname = ANY($1::text [ ])
	`, schemas).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("fixture schemas survived cleanup: %d %v", retained, err)
	}
}
