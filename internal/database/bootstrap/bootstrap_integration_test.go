package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// Un viaje cuyo shape salta 20 km en línea recta, como el 129H hacia Miserere,
// no puede dejar ese salto como tramo: el recorrido tiene que quedar sin ruta
// exportable en vez de ofrecer una recta que cruza la ciudad. Tampoco la isla
// de 4 km que el mismo cosido deja entre dos saltos, como la de 129H entre
// Calchaquí 1999 y Calchaquí 450. Una recta de 6 km de ruta que no linda con
// un salto, en cambio, es un tramo como cualquier otro, y también lo es un
// tramo corto pegado a uno.
func TestLaTransformacionGTFSDescartaLosSaltosDelShapeIntegration(t *testing.T) {
	// Fuera del AMBA, sobre el paralelo -40: 0,01 grados de longitud son 852 m.
	// La dirección 0 salta 0,24 grados (20,4 km), sigue con una recta de 0,05
	// grados (4,3 km), vuelve a saltar 0,24 grados y termina con un tramo de
	// 852 m. La dirección 1 termina con una recta de 0,07 grados (6 km).
	got := tramosTransformados(t, `
INSERT INTO vialis.gtfs_routes_raw (route_id, agency_id, route_short_name, route_desc)
VALUES ('fixture', '1', 'SALTO', '');

INSERT INTO vialis.gtfs_trips_raw (route_id, service_id, trip_id, trip_headsign, direction_id, shape_id)
VALUES
    ('fixture', '1', 'salto-0', 'a Oeste', 0, 'salto-0'),
    ('fixture', '1', 'salto-1', 'a Oeste', 1, 'salto-1');

INSERT INTO vialis.gtfs_stops_raw (stop_id, stop_code, stop_name, stop_lat, stop_lon)
VALUES
    ('p1', '', 'Uno', -40, -50.00),
    ('p2', '', 'Dos', -40, -50.01),
    ('p3', '', 'Tres', -40, -50.25),
    ('p4', '', 'Cuatro', -40, -50.30),
    ('p5', '', 'Cinco', -40, -50.54),
    ('p6', '', 'Seis', -40, -50.55),
    ('p7', '', 'Siete', -40, -50.56),
    ('p8', '', 'Ocho', -40, -50.63);

INSERT INTO vialis.gtfs_shapes_raw (shape_id, shape_pt_lat, shape_pt_lon, shape_pt_sequence)
VALUES
    ('salto-0', -40, -50.00, 1),
    ('salto-0', -40, -50.01, 2),
    ('salto-0', -40, -50.25, 3),
    ('salto-0', -40, -50.30, 4),
    ('salto-0', -40, -50.54, 5),
    ('salto-0', -40, -50.55, 6),
    ('salto-1', -40, -50.55, 1),
    ('salto-1', -40, -50.56, 2),
    ('salto-1', -40, -50.63, 3);

INSERT INTO vialis.gtfs_stop_times_raw (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
VALUES
    ('salto-0', '08:00:00', '08:00:00', 'p1', 1),
    ('salto-0', '08:02:00', '08:02:00', 'p2', 2),
    ('salto-0', '08:30:00', '08:30:00', 'p3', 3),
    ('salto-0', '08:36:00', '08:36:00', 'p4', 4),
    ('salto-0', '09:04:00', '09:04:00', 'p5', 5),
    ('salto-0', '09:06:00', '09:06:00', 'p6', 6),
    ('salto-1', '10:00:00', '10:00:00', 'p6', 1),
    ('salto-1', '10:02:00', '10:02:00', 'p7', 2),
    ('salto-1', '10:12:00', '10:12:00', 'p8', 3);
`)

	want := []string{
		"0/1:true", "0/2:false", "0/3:false", "0/4:false", "0/5:true", "0/6:false",
		"1/1:true", "1/2:true", "1/3:false",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("tramos = %v, want %v", got, want)
	}
}

// Un shape que da una vuelta de 7 km entre dos paradas a 340 m, como los 46 km
// entre dos paradas de 79J, recorre un lazo que ninguna parada del viaje usa: el
// tramo no puede quedar como camino. Un rulo de 3 km, del tamaño de los de
// cabecera, sí es un tramo.
func TestLaTransformacionGTFSDescartaLosLazosSinParadasIntegration(t *testing.T) {
	// Fuera del AMBA, sobre el paralelo -40: 0,004 grados de longitud son 340 m y
	// 0,03 grados de latitud, 3,3 km. La dirección 0 sube 3,3 km y baja entre la
	// primera y la segunda parada; la dirección 1 sube 1,4 km.
	got := tramosTransformados(t, `
INSERT INTO vialis.gtfs_routes_raw (route_id, agency_id, route_short_name, route_desc)
VALUES ('fixture', '1', 'LAZO', '');

INSERT INTO vialis.gtfs_trips_raw (route_id, service_id, trip_id, trip_headsign, direction_id, shape_id)
VALUES
    ('fixture', '1', 'lazo-0', 'a Oeste', 0, 'lazo-0'),
    ('fixture', '1', 'lazo-1', 'a Oeste', 1, 'lazo-1');

INSERT INTO vialis.gtfs_stops_raw (stop_id, stop_code, stop_name, stop_lat, stop_lon)
VALUES
    ('q1', '', 'Uno', -40, -50.000),
    ('q2', '', 'Dos', -40, -50.004),
    ('q3', '', 'Tres', -40, -50.014),
    ('r1', '', 'Cuatro', -40, -50.100),
    ('r2', '', 'Cinco', -40, -50.104),
    ('r3', '', 'Seis', -40, -50.114);

INSERT INTO vialis.gtfs_shapes_raw (shape_id, shape_pt_lat, shape_pt_lon, shape_pt_sequence)
VALUES
    ('lazo-0', -40.000, -50.000, 1),
    ('lazo-0', -39.970, -50.000, 2),
    ('lazo-0', -39.970, -50.004, 3),
    ('lazo-0', -40.000, -50.004, 4),
    ('lazo-0', -40.000, -50.014, 5),
    ('lazo-1', -40.000, -50.100, 1),
    ('lazo-1', -39.987, -50.100, 2),
    ('lazo-1', -39.987, -50.104, 3),
    ('lazo-1', -40.000, -50.104, 4),
    ('lazo-1', -40.000, -50.114, 5);

INSERT INTO vialis.gtfs_stop_times_raw (trip_id, arrival_time, departure_time, stop_id, stop_sequence)
VALUES
    ('lazo-0', '08:00:00', '08:00:00', 'q1', 1),
    ('lazo-0', '08:01:00', '08:01:00', 'q2', 2),
    ('lazo-0', '08:03:00', '08:03:00', 'q3', 3),
    ('lazo-1', '09:00:00', '09:00:00', 'r1', 1),
    ('lazo-1', '09:10:00', '09:10:00', 'r2', 2),
    ('lazo-1', '09:12:00', '09:12:00', 'r3', 3);
`)

	want := []string{
		"0/1:false", "0/2:true", "0/3:false",
		"1/1:true", "1/2:true", "1/3:false",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("tramos = %v, want %v", got, want)
	}
}

// tramosTransformados carga el fixture en un staging vacío, corre
// transformar_gtfs.sql y devuelve, para cada parada del route_id 'fixture',
// "dirección/parada:tiene tramo".
//
// transformar_gtfs.sql vacía las tablas finales, así que corre entero dentro de
// una transacción que termina en ROLLBACK, sobre un staging que la misma
// transacción recrea con sólo el fixture. Mientras dura toma locks exclusivos
// sobre las tablas de recorridos, pero no deja nada cambiado.
func tramosTransformados(t *testing.T, fixture string) []string {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	if _, err := connection.Exec(ctx, sinTransaccionPropia(t, scripts.CrearGTFSRaw)); err != nil {
		t.Fatalf("recreate staging: %v", err)
	}
	if _, err := connection.Exec(ctx, fixture); err != nil {
		t.Fatalf("insert fixture: %v", err)
	}
	if _, err := connection.Exec(ctx, sinTransaccionPropia(t, scripts.TransformarGTFS)); err != nil {
		t.Fatalf("run transformar_gtfs.sql: %v", err)
	}

	rows, err := connection.Query(ctx, `
SELECT r.direction_id, rp.nro_parada, rp.tramo_hasta_siguiente IS NOT NULL
FROM vialis.recorridos r
JOIN vialis.recorridos_paradas rp ON rp.id_recorrido = r.id_recorrido
WHERE r.gtfs_route_id = 'fixture'
ORDER BY r.direction_id, rp.nro_parada
`)
	if err != nil {
		t.Fatalf("read transformed stops: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var direction, stop int
		var hasPath bool
		if err := rows.Scan(&direction, &stop, &hasPath); err != nil {
			t.Fatalf("scan transformed stop: %v", err)
		}
		got = append(got, fmt.Sprintf("%d/%d:%t", direction, stop, hasPath))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate transformed stops: %v", err)
	}
	return got
}

// sinTransaccionPropia adapta un script del pipeline para correr dentro de la
// transacción de la prueba: saca sus directivas de psql, su BEGIN y su COMMIT
// —que confirmaría la transacción de afuera— y los VACUUM, que no pueden correr
// dentro de una.
func sinTransaccionPropia(t *testing.T, script string) string {
	t.Helper()
	lines := strings.Split(stripPsqlDirectives(script), "\n")
	var begins, commits int
	for index, line := range lines {
		switch strings.ToUpper(strings.TrimSpace(line)) {
		case "BEGIN;":
			begins++
		case "COMMIT;":
			commits++
		default:
			if !vacuumPattern.MatchString(line) {
				continue
			}
		}
		lines[index] = ""
	}
	if begins != 1 || commits != 1 {
		t.Fatalf("el script tiene %d BEGIN y %d COMMIT, se esperaba uno de cada uno", begins, commits)
	}
	return strings.Join(lines, "\n")
}
