package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	scripts "github.com/ManuelGarciaF/vialis-motor/sql"
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

// Las restricciones viajan por COPY a columnas jsonb; la prueba lo hace dentro de
// una transacción que termina en ROLLBACK, así el staging de la base no cambia.
func TestCopyStreetRestrictionsIntegration(t *testing.T) {
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

	executor := &executor{connection: connection.Conn(), logger: slog.New(slog.DiscardHandler)}
	if err := executor.runScript(ctx, scripts.CrearRestriccionesRaw); err != nil {
		t.Fatalf("create staging table: %v", err)
	}
	copied, err := executor.copyStreetRestrictions(ctx, []streetRestriction{
		{
			RelationID: 1435772,
			Tags:       map[string]string{"restriction": "no_left_turn", "type": "restriction"},
			Members: []restrictionMember{
				{Type: "way", Ref: 1452751314, Role: "from"},
				{Type: "node", Ref: 89318040, Role: "via"},
			},
		},
		{RelationID: 2, Tags: map[string]string{"type": "restriction"}},
	})
	if err != nil {
		t.Fatalf("copy restrictions: %v", err)
	}
	if copied != 2 {
		t.Fatalf("rows copied = %d, want 2", copied)
	}

	var restriction, viaRole string
	var viaRef int64
	var emptyMembers int
	err = connection.QueryRow(ctx, `
SELECT
    etiquetas ->> 'restriction',
    (miembros -> 1 ->> 'ref')::bigint,
    miembros -> 1 ->> 'rol',
    (SELECT jsonb_array_length(miembros) FROM vialis.calles_restricciones_raw WHERE osm_relation_id = 2)
FROM vialis.calles_restricciones_raw
WHERE osm_relation_id = 1435772
`).Scan(&restriction, &viaRef, &viaRole, &emptyMembers)
	if err != nil {
		t.Fatalf("read back the copied rows: %v", err)
	}
	if restriction != "no_left_turn" || viaRef != 89318040 || viaRole != "via" || emptyMembers != 0 {
		t.Errorf("read back %q, %d, %q, %d", restriction, viaRef, viaRole, emptyMembers)
	}
}

// Sobre la base cargada por cmd/initdb: la restricción r1435772 de Av. Luis
// María Campos (no_left_turn from w1452751314 via n89318040 to w10434820) tiene
// que quedar traducida a las aristas de esos ways que llegan al nodo y salen de
// él, y ninguna fila puede referir un giro que no pase por su vértice via.
func TestStreetRestrictionsAreMappedIntegration(t *testing.T) {
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

	var total, invalid int
	err = pool.QueryRow(ctx, `
SELECT
    count(*),
    count(*) FILTER (WHERE NOT (
        (desde.destino = r.id_vertice_via
         OR desde.origen = r.id_vertice_via AND desde.costo_inverso > 0)
        AND (hacia.origen = r.id_vertice_via
             OR hacia.destino = r.id_vertice_via AND hacia.costo_inverso > 0)
    ))
FROM vialis.calles_restricciones r
JOIN vialis.calles desde ON desde.id_calle = r.id_calle_desde
JOIN vialis.calles hacia ON hacia.id_calle = r.id_calle_hacia
`).Scan(&total, &invalid)
	if err != nil {
		t.Fatalf("count restrictions: %v", err)
	}
	if total == 0 {
		t.Fatal("vialis.calles_restricciones está vacía")
	}
	if invalid != 0 {
		t.Errorf("%d restricciones no pasan por su vértice via", invalid)
	}

	var restriction string
	var fromWay, toWay, viaNode int64
	err = pool.QueryRow(ctx, `
SELECT r.restriccion, desde.osm_way_id, hacia.osm_way_id, via.osm_node_id
FROM vialis.calles_restricciones r
JOIN vialis.calles desde ON desde.id_calle = r.id_calle_desde
JOIN vialis.calles hacia ON hacia.id_calle = r.id_calle_hacia
JOIN vialis.calles_vertices via ON via.id_vertice = r.id_vertice_via
WHERE r.osm_relation_id = 1435772
`).Scan(&restriction, &fromWay, &toWay, &viaNode)
	if err != nil {
		t.Fatalf("read r1435772: %v", err)
	}
	if restriction != "no_left_turn" || fromWay != 1452751314 || toWay != 10434820 || viaNode != 89318040 {
		t.Errorf("r1435772 = %s w%d -> n%d -> w%d", restriction, fromWay, viaNode, toWay)
	}
}
