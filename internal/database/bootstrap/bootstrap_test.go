package bootstrap

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	scripts "github.com/ManuelGarciaF/vialis-motor/sql"
)

// El orden es el contrato del paquete: cada paso consume lo que produjo el
// anterior. Se fija acá para que reordenarlo sea una decisión y no un descuido.
func TestStepNamesAreInPipelineOrder(t *testing.T) {
	expected := []string{
		"esquema y extensiones (sql/init_db.sql)",
		"tablas finales (sql/ddl.sql)",
		"red vial OpenStreetMap",
		"tablas de staging GTFS (sql/recorridos/crear_gtfs_raw.sql)",
		"importación de los siete archivos GTFS",
		"transformación GTFS (sql/recorridos/transformar_gtfs.sql)",
		"conexiones entre recorridos (sql/recorridos/conexiones_recorridos.sql)",
		"importación del CSV de viajes",
		"transformación de viajes (sql/viajes/transformar_viajes.sql)",
		"hexágonos H3 (sql/viajes/hexagonos_viajes.sql)",
		"matriz origen-destino (sql/viajes/matriz_origen_destino.sql)",
		"combinaciones O-D por banda horaria (sql/viajes/combinaciones_od.sql)",
		"ranking de combinaciones de líneas (sql/viajes/combinaciones_lineas.sql)",
		"cuadro tarifario (sql/tarifas/insertar_tarifas_vigentes.sql)",
	}
	definitions := steps()
	names := make([]string, len(definitions))
	for index, definition := range definitions {
		names[index] = definition.name
	}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("pasos = %q, se esperaba %q", names, expected)
	}
}

func TestEveryStepIsRunnable(t *testing.T) {
	for index, definition := range steps() {
		if definition.run == nil {
			t.Errorf("el paso %d (%s) no tiene acción", index+1, definition.name)
		}
	}
}

func TestGTFSFilesMapToTheDocumentedStagingTables(t *testing.T) {
	expected := map[string]string{
		"agency.txt":         "vialis.gtfs_agency_raw",
		"routes.txt":         "vialis.gtfs_routes_raw",
		"trips.txt":          "vialis.gtfs_trips_raw",
		"stops.txt":          "vialis.gtfs_stops_raw",
		"stop_times.txt":     "vialis.gtfs_stop_times_raw",
		"shapes.txt":         "vialis.gtfs_shapes_raw",
		"calendar_dates.txt": "vialis.gtfs_calendar_dates_raw",
	}
	if len(gtfsFiles) != len(expected) {
		t.Fatalf("archivos GTFS = %d, se esperaban %d", len(gtfsFiles), len(expected))
	}
	seen := make(map[string]bool, len(gtfsFiles))
	for _, file := range gtfsFiles {
		table, known := expected[file.FileName]
		if !known {
			t.Errorf("archivo GTFS inesperado %q", file.FileName)
			continue
		}
		if file.Table != table {
			t.Errorf("%s se importa en %q, se esperaba %q", file.FileName, file.Table, table)
		}
		if seen[file.FileName] {
			t.Errorf("%s aparece dos veces", file.FileName)
		}
		seen[file.FileName] = true
		if len(file.Columns) == 0 {
			t.Errorf("%s no declara columnas", file.FileName)
		}
	}
}

// Las columnas tienen que estar en el mismo orden que el encabezado del archivo
// y que el CREATE TABLE; si se corren una posición, los valores entran en la
// columna equivocada sin que nadie falle.
func TestGTFSColumnsFollowTheStagingDDL(t *testing.T) {
	expected := map[string][]string{
		"agency.txt": {
			"agency_id", "agency_name", "agency_url",
			"agency_timezone", "agency_lang", "agency_phone",
		},
		"routes.txt": {
			"route_id", "agency_id", "route_short_name",
			"route_long_name", "route_desc", "route_type",
		},
		"trips.txt": {
			"route_id", "service_id", "trip_id", "trip_headsign",
			"trip_short_name", "direction_id", "block_id", "shape_id", "exceptional",
		},
		"stops.txt": {"stop_id", "stop_code", "stop_name", "stop_lat", "stop_lon"},
		"stop_times.txt": {
			"trip_id", "arrival_time", "departure_time", "stop_id",
			"stop_sequence", "timepoint", "shape_dist_traveled",
		},
		"shapes.txt": {
			"shape_id", "shape_pt_lat", "shape_pt_lon",
			"shape_pt_sequence", "shape_dist_traveled",
		},
		"calendar_dates.txt": {"service_id", "date", "exception_type"},
	}
	for _, file := range gtfsFiles {
		if names := file.columnNames(); !reflect.DeepEqual(names, expected[file.FileName]) {
			t.Errorf("%s: columnas %q, se esperaban %q", file.FileName, names, expected[file.FileName])
		}
	}
}

func TestViajesFileMapsToItsStagingTable(t *testing.T) {
	if viajesFile.Table != "vialis.viajes_raw" {
		t.Errorf("tabla = %q, se esperaba vialis.viajes_raw", viajesFile.Table)
	}
	expected := []string{
		"id_tarjeta", "id_viaje", "cantidad_etapas", "rango_horario",
		"etapas_subte", "etapas_tren", "etapas_colectivo",
		"longitud_origen_viaje", "latitud_origen_viaje",
		"longitud_destino_viaje", "latitud_destino_viaje",
		"departamento_origen_viaje", "departamento_destino_viaje",
		"factor_expansion_viaje", "etapas_incompletas", "genero", "grupo_edad",
	}
	if names := viajesFile.columnNames(); !reflect.DeepEqual(names, expected) {
		t.Errorf("columnas = %q, se esperaban %q", names, expected)
	}
}

func TestResolveExistingSchema(t *testing.T) {
	cases := []struct {
		name       string
		exists     bool
		reset      bool
		wantDrop   bool
		wantFailed bool
	}{
		{name: "base vacía", exists: false, reset: false},
		{name: "base vacía con reset", exists: false, reset: true},
		{name: "base poblada sin reset", exists: true, reset: false, wantFailed: true},
		{name: "base poblada con reset", exists: true, reset: true, wantDrop: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			drop, err := resolveExistingSchema(testCase.exists, testCase.reset)
			if testCase.wantFailed {
				if !errors.Is(err, ErrDatabaseAlreadyInitialized) {
					t.Fatalf("error = %v, se esperaba ErrDatabaseAlreadyInitialized", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error inesperado: %v", err)
			}
			if drop != testCase.wantDrop {
				t.Errorf("drop = %t, se esperaba %t", drop, testCase.wantDrop)
			}
		})
	}
}

func TestStreetTransformReplacesPsqlVariables(t *testing.T) {
	path := t.TempDir() + "/calles.osm"
	if err := os.WriteFile(path, []byte("osm fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := streetTransformScript(path, "2026-08-27T20:21:06Z", "3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, ":'") {
		t.Fatal("el script conserva variables de psql")
	}
	for _, value := range []string{"calles.osm", "2026-08-27T20:21:06Z", "3.0.0"} {
		if !strings.Contains(script, value) {
			t.Errorf("el script no contiene %q", value)
		}
	}
}

func TestStripPsqlDirectives(t *testing.T) {
	script := "\\set ON_ERROR_STOP on\n  \\timing off\nSELECT 1;\n-- \\set no es directiva acá\n"
	expected := "\n\nSELECT 1;\n-- \\set no es directiva acá\n"
	if got := stripPsqlDirectives(script); got != expected {
		t.Fatalf("script = %q, se esperaba %q", got, expected)
	}
}

// Todos los scripts embebidos que el pipeline ejecuta tienen que poder mandarse
// al servidor sin directivas de psql y ninguno puede estar vacío.
func TestEmbeddedScriptsAreExecutable(t *testing.T) {
	embedded := map[string]string{
		"init_db.sql":                   scripts.InitDB,
		"ddl.sql":                       scripts.DDL,
		"crear_gtfs_raw.sql":            scripts.CrearGTFSRaw,
		"transformar_gtfs.sql":          scripts.TransformarGTFS,
		"transformar_calles.sql":        scripts.TransformarCalles,
		"mapconfig.xml":                 scripts.CallesMapConfig,
		"amba-margen-10km.geojson":      scripts.CallesScope,
		"crear_viajes_raw.sql":          scripts.CrearViajesRaw,
		"transformar_viajes.sql":        scripts.TransformarViajes,
		"hexagonos_viajes.sql":          scripts.HexagonosViajes,
		"matriz_origen_destino.sql":     scripts.MatrizOrigenDestino,
		"insertar_tarifas_vigentes.sql": scripts.InsertarTarifasVigentes,
	}
	for name, text := range embedded {
		if strings.TrimSpace(text) == "" {
			t.Errorf("%s está vacío", name)
			continue
		}
		for _, line := range strings.Split(stripPsqlDirectives(text), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), `\`) {
				t.Errorf("%s conserva la directiva %q", name, line)
			}
		}
	}
}

// Los scripts terminan en VACUUM ANALYZE, y PostgreSQL no lo acepta dentro de
// una cadena de varias sentencias: cada uno tiene que viajar solo.
func TestSplitStandaloneStatementsIsolatesVacuum(t *testing.T) {
	script := "BEGIN;\nINSERT INTO t VALUES (1);\nCOMMIT;\n\nVACUUM ANALYZE t;\n  vacuum analyze u;\n-- VACUUM ANALYZE v; no es una sentencia\nSELECT 1;\n"
	expected := []string{
		"BEGIN;\nINSERT INTO t VALUES (1);\nCOMMIT;\n",
		"VACUUM ANALYZE t;",
		"vacuum analyze u;",
		"-- VACUUM ANALYZE v; no es una sentencia\nSELECT 1;\n",
	}
	if chunks := splitStandaloneStatements(script); !reflect.DeepEqual(chunks, expected) {
		t.Fatalf("fragmentos = %q, se esperaba %q", chunks, expected)
	}
}

func TestSplitStandaloneStatementsDropsEmptyFragments(t *testing.T) {
	if chunks := splitStandaloneStatements("\n\n   \n"); chunks != nil {
		t.Fatalf("fragmentos = %q, se esperaba ninguno", chunks)
	}
}

// Los dos scripts que traen VACUUM tienen que quedar partidos; el resto viaja
// entero.
func TestPipelineScriptsWithVacuumAreSplit(t *testing.T) {
	if chunks := splitStandaloneStatements(scripts.TransformarGTFS); len(chunks) < 2 {
		t.Errorf("transformar_gtfs.sql quedó en %d fragmento(s)", len(chunks))
	}
	if chunks := splitStandaloneStatements(scripts.TransformarViajes); len(chunks) < 2 {
		t.Errorf("transformar_viajes.sql quedó en %d fragmento(s)", len(chunks))
	}
	if chunks := splitStandaloneStatements(scripts.DDL); len(chunks) != 1 {
		t.Errorf("ddl.sql quedó en %d fragmentos, se esperaba 1", len(chunks))
	}
}

// Los dos agregados de H0009 dependen de pasos anteriores y no al reves: el
// grafo de trasbordos necesita recorridos y paradas, y las combinaciones O-D
// necesitan las celdas H3 y los hexagonos a los que la tabla referencia. Fijar
// las posiciones relativas evita que una reordenacion futura los adelante a
// tablas todavia vacias, que no falla: produce silenciosamente cero filas.
func TestLosAgregadosDeCombinacionesVanDespuesDeSusDependencias(t *testing.T) {
	posicion := make(map[string]int)
	for index, definition := range steps() {
		posicion[definition.name] = index
	}

	dependencias := []struct{ antes, despues string }{
		{
			antes:   "transformación GTFS (sql/recorridos/transformar_gtfs.sql)",
			despues: "conexiones entre recorridos (sql/recorridos/conexiones_recorridos.sql)",
		},
		{
			antes:   "transformación de viajes (sql/viajes/transformar_viajes.sql)",
			despues: "combinaciones O-D por banda horaria (sql/viajes/combinaciones_od.sql)",
		},
		{
			antes:   "hexágonos H3 (sql/viajes/hexagonos_viajes.sql)",
			despues: "combinaciones O-D por banda horaria (sql/viajes/combinaciones_od.sql)",
		},
		// El ranking cruza los dos dominios: necesita los flujos con trasbordo
		// y el grafo de trasbordos, así que va después de los dos.
		{
			antes:   "combinaciones O-D por banda horaria (sql/viajes/combinaciones_od.sql)",
			despues: "ranking de combinaciones de líneas (sql/viajes/combinaciones_lineas.sql)",
		},
		{
			antes:   "conexiones entre recorridos (sql/recorridos/conexiones_recorridos.sql)",
			despues: "ranking de combinaciones de líneas (sql/viajes/combinaciones_lineas.sql)",
		},
	}
	for _, dependencia := range dependencias {
		antes, existeAntes := posicion[dependencia.antes]
		despues, existeDespues := posicion[dependencia.despues]
		if !existeAntes || !existeDespues {
			t.Fatalf(
				"falta un paso del par (%q, %q)",
				dependencia.antes,
				dependencia.despues,
			)
		}
		if antes >= despues {
			t.Errorf(
				"%q está en la posición %d y tiene que correr antes que %q, en la %d",
				dependencia.antes, antes, dependencia.despues, despues,
			)
		}
	}
}

// Los scripts de los dos agregados vacían su tabla antes de recalcularla. Sin
// eso, una segunda corrida del pipeline sobre una base ya poblada choca contra
// la clave primaria en lugar de actualizar los datos.
func TestLaUltimaBandaDeCadaJurisdiccionNoTieneLimiteSuperior(t *testing.T) {
	script := strings.Join(strings.Fields(scripts.InsertarTarifasVigentes), " ")
	for _, row := range []string{
		"('caba', 12000, NULL",
		"('province', 27000, NULL",
		"('national', 27000, NULL",
	} {
		if !strings.Contains(script, row) {
			t.Errorf("falta la banda abierta %q", row)
		}
	}
}

func TestLosAgregadosDeCombinacionesSePuedenRecalcular(t *testing.T) {
	casos := map[string]string{
		"conexiones entre recorridos":        scripts.ConexionesRecorridos,
		"combinaciones O-D":                  scripts.CombinacionesOD,
		"ranking de combinaciones de líneas": scripts.CombinacionesLineas,
	}
	for nombre, script := range casos {
		if !strings.Contains(strings.ToUpper(script), "TRUNCATE") {
			t.Errorf("el script de %s no vacía su tabla antes de recalcularla", nombre)
		}
	}
}

// transformar_gtfs.sql reemplaza recorridos y paradas con un TRUNCATE, y
// conexiones_recorridos las referencia con claves foráneas. Postgres rechaza
// truncar una tabla referenciada si la que la referencia no viaja en la misma
// sentencia, así que dejarla afuera rompe el pipeline entero en el paso 5. Pasó:
// la primera corrida de la carga completa murió ahí con SQLSTATE 0A000.
func TestLaTransformacionGTFSVaciaLasConexionesQueLaReferencian(t *testing.T) {
	truncate := strings.Index(strings.ToUpper(scripts.TransformarGTFS), "TRUNCATE TABLE")
	if truncate < 0 {
		t.Fatal("transformar_gtfs.sql ya no hace TRUNCATE: revisar esta invariante")
	}
	fin := strings.Index(scripts.TransformarGTFS[truncate:], ";")
	if fin < 0 {
		t.Fatal("el TRUNCATE de transformar_gtfs.sql no termina en punto y coma")
	}
	sentencia := scripts.TransformarGTFS[truncate : truncate+fin]
	if !strings.Contains(sentencia, "vialis.conexiones_recorridos") {
		t.Error(
			"el TRUNCATE de transformar_gtfs.sql no incluye vialis.conexiones_recorridos, " +
				"que referencia recorridos y paradas: el paso 5 va a fallar con SQLSTATE 0A000",
		)
	}
}
