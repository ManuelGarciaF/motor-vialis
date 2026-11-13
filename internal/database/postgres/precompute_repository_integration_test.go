package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/precompute"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Route all repository statements to a session-local fixture: never change
// the real catalog, even if TEST_DATABASE_URL points at a populated database.
type precomputeTestTx struct{ pgx.Tx }

func (tx precomputeTestTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return tx.Tx.Exec(ctx, strings.ReplaceAll(sql, "vialis.recorridos", "pg_temp.precompute_recorridos"), args...)
}
func (tx precomputeTestTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return tx.Tx.Query(ctx, strings.ReplaceAll(sql, "vialis.recorridos", "pg_temp.precompute_recorridos"), args...)
}
func (tx precomputeTestTx) Begin(ctx context.Context) (pgx.Tx, error) {
	nested, err := tx.Tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return precomputeTestTx{nested}, nil
}

func TestPrecomputeRepositoryIntegration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	_, err = tx.Exec(ctx, `CREATE TEMP TABLE precompute_recorridos(
 id_recorrido bigint PRIMARY KEY,
 caudal_pasajeros integer CHECK(caudal_pasajeros>=0),
 ingreso_economico bigint CHECK(ingreso_economico>=0),
 tiempo_total_minutos smallint,
 destino text
 );
 INSERT INTO precompute_recorridos VALUES
 (1,NULL,NULL,69,'Morón'),(2,12,NULL,50,'Buenos Aires'),(3,25,2500,45,'Retiro');`)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewPrecomputeRepository(precomputeTestTx{tx})
	targets, err := repo.Targets(ctx, 0, 0, false)
	if err != nil || len(targets) != 3 {
		t.Fatalf("targets=%v err=%v", targets, err)
	}
	missing, err := repo.Targets(ctx, 0, 0, true)
	if err != nil || len(missing) != 2 {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
	page, err := repo.Targets(ctx, 1, 1, false)
	if err != nil || len(page) != 1 || page[0].ID != 2 {
		t.Fatalf("page=%v err=%v", page, err)
	}
	if err := repo.Save(ctx, targets[0], precompute.Metrics{Passengers: 124, Revenue: 457}); err != nil {
		t.Fatal(err)
	}
	var passengers, revenue, minutes int64
	var destination string
	err = tx.QueryRow(ctx, `SELECT caudal_pasajeros,ingreso_economico,tiempo_total_minutos,destino FROM pg_temp.precompute_recorridos WHERE id_recorrido=1`).Scan(&passengers, &revenue, &minutes, &destination)
	if err != nil || passengers != 124 || revenue != 457 || minutes != 69 || destination != "Morón" {
		t.Fatalf("%d %d %d %s %v", passengers, revenue, minutes, destination, err)
	}
	if err := repo.Save(ctx, targets[0], precompute.Metrics{Passengers: 1, Revenue: 1}); err == nil {
		t.Fatal("overwrote concurrently changed values")
	}
	if err := repo.Save(ctx, targets[1], precompute.Metrics{Passengers: 55, Revenue: -1}); err == nil {
		t.Fatal("accepted negative revenue")
	}
	var oldPassengers int64
	var oldRevenue *int64
	if err := tx.QueryRow(ctx, `SELECT caudal_pasajeros,ingreso_economico FROM pg_temp.precompute_recorridos WHERE id_recorrido=2`).Scan(&oldPassengers, &oldRevenue); err != nil {
		t.Fatal(err)
	}
	if oldPassengers != 12 || oldRevenue != nil {
		t.Fatal("partial save after failure")
	}
}
