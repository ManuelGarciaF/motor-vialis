package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Verifica la carga por COPY contra un PostgreSQL real, que es donde se ve si
// los tipos que arma convert() entran en las columnas del staging.
//
// La prueba no ejecuta el pipeline: lo hace todo dentro de una transacción que
// termina en ROLLBACK y sobre una tabla temporal, porque inicializar es
// destructivo y una prueba no puede borrar la base de quien la corre. Tampoco
// necesita el dataset completo: lo que se verifica son los tipos y el orden de
// las columnas, no el volumen.
func TestCopyFileIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "BEGIN"); err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer func() { _, _ = connection.Exec(context.Background(), "ROLLBACK") }()

	const table = "stops_raw_prueba"
	_, err = connection.Exec(ctx, `
		CREATE TEMP TABLE `+table+` (
			stop_id   TEXT,
			stop_code TEXT,
			stop_name TEXT,
			stop_lat  DOUBLE PRECISION,
			stop_lon  DOUBLE PRECISION
		) ON COMMIT DROP
	`)
	if err != nil {
		t.Fatalf("create temporary table: %v", err)
	}

	directory := t.TempDir()
	path := filepath.Join(directory, "stops.txt")
	content := "stop_id,stop_code,stop_name,stop_lat,stop_lon\r\n" +
		"1,A,Retiro,-34.591,-58.374\r\n" +
		"2,,Once,,-58.406\r\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	file := csvFile{FileName: "stops.txt", Table: table, Columns: gtfsFiles[3].Columns}
	executor := &executor{
		connection:    connection.Conn(),
		logger:        slog.New(slog.DiscardHandler),
		dataDirectory: directory,
	}

	copied, err := executor.copyFile(ctx, file, path)
	if err != nil {
		t.Fatalf("copy file: %v", err)
	}
	if copied != 2 {
		t.Fatalf("rows copied = %d, want 2", copied)
	}

	var name string
	var latitude *float64
	err = connection.QueryRow(
		ctx,
		"SELECT stop_name, stop_lat FROM "+table+" WHERE stop_id = '2'",
	).Scan(&name, &latitude)
	if err != nil {
		t.Fatalf("read back the copied row: %v", err)
	}
	if name != "Once" {
		t.Errorf("stop_name = %q, want %q", name, "Once")
	}
	if latitude != nil {
		t.Errorf("stop_lat = %v, want NULL", *latitude)
	}
}

// El esquema vialis se consulta en cada arranque; acá sólo se comprueba que la
// consulta corra contra un servidor real.
func TestSchemaExistsIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer pool.Close()

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer connection.Release()

	executor := &executor{connection: connection.Conn(), logger: slog.New(slog.DiscardHandler)}
	if _, err := executor.schemaExists(ctx); err != nil {
		t.Fatalf("schemaExists: %v", err)
	}
}
