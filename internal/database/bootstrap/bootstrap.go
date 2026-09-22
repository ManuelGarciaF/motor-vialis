// Package bootstrap builds the Vialis database from an empty one: schema,
// extensions, tables, data loads and every transformation, in the one order
// that produces a database the engine can query.
//
// The order lives here, in code, and not in a README, because it is the part of
// the pipeline that cannot be rearranged: recorridos needs its staging tables
// loaded, viajes needs its points before its H3 cells, the OD matrix needs the
// cells, the transfer graph needs recorridos and paradas, and the OD
// combinations need both the cells and the hexagons they reference. sql/viajes/README.md and sql/recorridos/README.md explain what each
// script does; steps() is what actually runs them.
package bootstrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	scripts "github.com/ManuelGarciaF/vialis-motor/sql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaName is the schema the whole pipeline writes into. Its presence is what
// tells the initializer that a database is already populated.
const SchemaName = "vialis"

// progressInterval is how often a load of several gigabytes reports how far it
// got. It is short enough that the process never looks hung and long enough that
// the report is not itself part of the cost.
const progressInterval = 15 * time.Second

// ErrDatabaseAlreadyInitialized is returned when the vialis schema already
// exists and the caller did not ask for a reset. Dropping data is never the
// default: the schema holds a load that takes minutes to rebuild.
var ErrDatabaseAlreadyInitialized = errors.New(
	"el esquema " + SchemaName + " ya existe: usá --reset para borrarlo y volver a crearlo",
)

// Options configures the inputs and destination of a full load.
type Options struct {
	// DataDirectory contains viajes_BAdata_20241016.csv and colectivos-gtfs/.
	DataDirectory string

	// StreetsFile is the canonical AMBA calles.osm extract.
	StreetsFile string

	// DatabaseURL is passed to osm2pgrouting for its staging import.
	DatabaseURL string

	// Reset drops the vialis schema before rebuilding it.
	Reset bool

	// Logger receives one line per step. Nil logs nothing.
	Logger *slog.Logger
}

// step is one indivisible piece of the pipeline. Steps are values and not a
// sequence of calls so their order can be read, and tested, without running any
// of them.
type step struct {
	name string
	run  func(ctx context.Context, e *executor) error
}

// steps returns the pipeline in execution order.
func steps() []step {
	return []step{
		{
			name: "esquema y extensiones (sql/init_db.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.InitDB)
			},
		},
		{
			name: "tablas finales (sql/ddl.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.DDL)
			},
		},
		{
			name: "red vial OpenStreetMap",
			run: func(ctx context.Context, e *executor) error {
				return e.importStreets(ctx)
			},
		},
		{
			name: "tablas de staging GTFS (sql/recorridos/crear_gtfs_raw.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.CrearGTFSRaw)
			},
		},
		{
			name: "importación de los siete archivos GTFS",
			run: func(ctx context.Context, e *executor) error {
				for _, file := range gtfsFiles {
					path := filepath.Join(e.dataDirectory, GTFSDirectory, file.FileName)
					if _, err := e.copyFile(ctx, file, path); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			name: "transformación GTFS (sql/recorridos/transformar_gtfs.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.TransformarGTFS)
			},
		},
		{
			name: "conexiones entre recorridos (sql/recorridos/conexiones_recorridos.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.ConexionesRecorridos)
			},
		},
		{
			name: "importación del CSV de viajes",
			run: func(ctx context.Context, e *executor) error {
				if err := e.runScript(ctx, scripts.CrearViajesRaw); err != nil {
					return err
				}
				path := filepath.Join(e.dataDirectory, viajesFile.FileName)
				_, err := e.copyFile(ctx, viajesFile, path)
				return err
			},
		},
		{
			name: "transformación de viajes (sql/viajes/transformar_viajes.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.TransformarViajes)
			},
		},
		{
			name: "hexágonos H3 (sql/viajes/hexagonos_viajes.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.HexagonosViajes)
			},
		},
		{
			name: "matriz origen-destino (sql/viajes/matriz_origen_destino.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.MatrizOrigenDestino)
			},
		},
		{
			name: "combinaciones O-D por banda horaria (sql/viajes/combinaciones_od.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.CombinacionesOD)
			},
		},
		{
			name: "ranking de combinaciones de líneas (sql/viajes/combinaciones_lineas.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.CombinacionesLineas)
			},
		},
		{
			name: "cuadro tarifario (sql/tarifas/insertar_tarifas_vigentes.sql)",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.InsertarTarifasVigentes)
			},
		},
	}
}

// Run builds the database. It holds a single connection for the whole run
// because the pipeline needs one: transformar_gtfs.sql uses temporary tables
// with ON COMMIT DROP, which only exist in the session that created them.
func Run(ctx context.Context, pool *pgxpool.Pool, options Options) error {
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("tomar una conexión del pool: %w", err)
	}
	defer connection.Release()

	e := &executor{
		connection:    connection.Conn(),
		logger:        logger,
		dataDirectory: options.DataDirectory,
		streetsFile:   options.StreetsFile,
		databaseURL:   options.DatabaseURL,
	}

	if err := e.checkFiles(); err != nil {
		return err
	}
	if err := e.prepareSchema(ctx, options.Reset); err != nil {
		return err
	}

	start := time.Now()
	for index, definition := range steps() {
		number := index + 1
		logger.Info("paso iniciado", "paso", number, "nombre", definition.name)
		stepStart := time.Now()
		if err := definition.run(ctx, e); err != nil {
			return fmt.Errorf("paso %d (%s): %w", number, definition.name, err)
		}
		logger.Info(
			"paso terminado",
			"paso", number,
			"nombre", definition.name,
			"duracion", time.Since(stepStart).Round(time.Millisecond).String(),
		)
	}
	logger.Info(
		"base de datos inicializada",
		"duracion", time.Since(start).Round(time.Second).String(),
	)
	return nil
}

// executor carries what every step needs: the session, where the data is, and
// where to report.
type executor struct {
	connection    *pgx.Conn
	logger        *slog.Logger
	dataDirectory string
	streetsFile   string
	databaseURL   string
}

// checkFiles verifies that every file the pipeline reads exists before the first
// one is created in the database. Finding out that stop_times.txt is missing
// after loading the trip survey would cost the user the whole load.
func (e *executor) checkFiles() error {
	for _, command := range []string{osm2pgroutingExecutable(), "osmium"} {
		if _, err := exec.LookPath(command); err != nil {
			return fmt.Errorf("falta %s en PATH: %w", command, err)
		}
	}
	if _, err := pgx.ParseConfig(e.databaseURL); err != nil {
		return fmt.Errorf("interpretar DATABASE_URL para osm2pgrouting: %w", err)
	}

	paths := []string{filepath.Join(e.dataDirectory, viajesFile.FileName), e.streetsFile}
	for _, file := range gtfsFiles {
		paths = append(paths, filepath.Join(e.dataDirectory, GTFSDirectory, file.FileName))
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf(
				"falta un archivo de datos (%w). "+
					"Los archivos que no se versionan se consiguen aparte: "+
					"ver \"Datos de entrada\" en el README",
				err,
			)
		}
	}
	return nil
}

func osm2pgroutingExecutable() string {
	if executable := os.Getenv("OSM2PGROUTING"); executable != "" {
		return executable
	}
	return "osm2pgrouting"
}

func (e *executor) importStreets(ctx context.Context) error {
	config, err := pgx.ParseConfig(e.databaseURL)
	if err != nil {
		return fmt.Errorf("interpretar DATABASE_URL para osm2pgrouting: %w", err)
	}

	mapConfig, err := os.CreateTemp("", "vialis-mapconfig-*.xml")
	if err != nil {
		return fmt.Errorf("crear configuración de osm2pgrouting: %w", err)
	}
	mapConfigPath := mapConfig.Name()
	defer os.Remove(mapConfigPath)
	if _, err := mapConfig.WriteString(scripts.CallesMapConfig); err != nil {
		mapConfig.Close()
		return fmt.Errorf("escribir configuración de osm2pgrouting: %w", err)
	}
	if err := mapConfig.Close(); err != nil {
		return fmt.Errorf("cerrar configuración de osm2pgrouting: %w", err)
	}

	versionOutput, err := exec.CommandContext(
		ctx, osm2pgroutingExecutable(), "--version",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("consultar versión de osm2pgrouting: %w", err)
	}
	dataTimeOutput, err := exec.CommandContext(
		ctx,
		"osmium", "fileinfo", "-e", "-g", "data.timestamp.last", e.streetsFile,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("consultar fecha de datos OSM con osmium: %w", err)
	}
	transform, err := streetTransformScript(
		e.streetsFile,
		strings.TrimSpace(string(dataTimeOutput)),
		strings.TrimSpace(string(versionOutput)),
	)
	if err != nil {
		return err
	}

	command := exec.CommandContext(ctx, osm2pgroutingExecutable(),
		"--file", e.streetsFile,
		"--conf", mapConfigPath,
		"--dbname", config.Database,
		"--username", config.User,
		"--password", config.Password,
		"--host", config.Host,
		"--port", fmt.Sprint(config.Port),
		"--schema", SchemaName,
		"--prefix", "calles_",
		"--suffix", "_raw",
		"--addnodes",
		"--tags",
		"--clean",
	)
	command.Stdout = os.Stderr
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("importar red vial con osm2pgrouting: %w", err)
	}
	return e.runScript(ctx, transform)
}

func streetTransformScript(streetsFile, dataTime, toolVersion string) (string, error) {
	file, err := os.Open(streetsFile)
	if err != nil {
		return "", fmt.Errorf("abrir %s: %w", streetsFile, err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		file.Close()
		return "", fmt.Errorf("calcular checksum de %s: %w", streetsFile, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("cerrar %s: %w", streetsFile, err)
	}
	info, err := os.Stat(streetsFile)
	if err != nil {
		return "", fmt.Errorf("consultar %s: %w", streetsFile, err)
	}

	var scope struct {
		Features []struct {
			Geometry json.RawMessage `json:"geometry"`
		} `json:"features"`
	}
	if err := json.Unmarshal([]byte(scripts.CallesScope), &scope); err != nil {
		return "", fmt.Errorf("interpretar alcance vial embebido: %w", err)
	}
	if len(scope.Features) != 1 || len(scope.Features[0].Geometry) == 0 {
		return "", errors.New("el alcance vial embebido debe contener una geometría")
	}

	if dataTime == "" || toolVersion == "" {
		return "", errors.New("faltan la fecha del extracto OSM o la versión de osm2pgrouting")
	}
	values := map[string]string{
		"fuente_url":          "archivo local: " + filepath.Base(streetsFile),
		"fecha_descarga":      info.ModTime().UTC().Format(time.RFC3339),
		"fecha_datos":         dataTime,
		"checksum_sha256":     hex.EncodeToString(hash.Sum(nil)),
		"alcance_geojson":     string(scope.Features[0].Geometry),
		"version_herramienta": toolVersion,
	}
	transform := scripts.TransformarCalles
	for name, value := range values {
		transform = strings.ReplaceAll(transform, ":'"+name+"'", postgresLiteral(value))
	}
	return transform, nil
}

func postgresLiteral(value string) string {
	tag := "$vialis$"
	for strings.Contains(value, tag) {
		tag = "$" + tag
	}
	return tag + value + tag
}

// prepareSchema applies the guard against running over a populated database.
func (e *executor) prepareSchema(ctx context.Context, reset bool) error {
	exists, err := e.schemaExists(ctx)
	if err != nil {
		return err
	}
	drop, err := resolveExistingSchema(exists, reset)
	if err != nil {
		return err
	}
	if !drop {
		return nil
	}
	e.logger.Warn("borrando el esquema existente", "esquema", SchemaName)
	_, err = e.connection.Exec(ctx, "DROP SCHEMA "+SchemaName+" CASCADE")
	if err != nil {
		return fmt.Errorf("borrar el esquema %s: %w", SchemaName, err)
	}
	return nil
}

// resolveExistingSchema decides what to do with a schema that is already there.
// It is separate from the query so the rule can be read, and tested, on its own.
func resolveExistingSchema(exists, reset bool) (drop bool, err error) {
	if !exists {
		return false, nil
	}
	if !reset {
		return false, ErrDatabaseAlreadyInitialized
	}
	return true, nil
}

func (e *executor) schemaExists(ctx context.Context) (bool, error) {
	var exists bool
	err := e.connection.QueryRow(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)",
		SchemaName,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("consultar si existe el esquema %s: %w", SchemaName, err)
	}
	return exists, nil
}

// runScript sends a .sql file to the server the way psql would, on a single
// session: everything but the VACUUMs travels as one command string, which is
// how each script's BEGIN/COMMIT and its temporary tables keep meaning what they
// say.
func (e *executor) runScript(ctx context.Context, script string) error {
	for _, chunk := range splitStandaloneStatements(stripPsqlDirectives(script)) {
		if _, err := e.connection.Exec(ctx, chunk); err != nil {
			return err
		}
	}
	return nil
}

// vacuumPattern matches a VACUUM that is a statement of its own, which is the
// only shape the scripts use.
var vacuumPattern = regexp.MustCompile(`(?i)^\s*VACUUM\b[^;]*;\s*$`)

// splitStandaloneStatements cuts a script so that every VACUUM ends up alone in
// its own command string. PostgreSQL treats a string of several commands as one
// implicit transaction, and VACUUM cannot run inside a transaction block, so a
// script that ends in VACUUM ANALYZE — as transformar_gtfs.sql and
// transformar_viajes.sql do — fails outright if sent whole. psql does not hit
// this because it sends one command at a time.
//
// Everything between two VACUUMs stays together: splitting a script into
// statements would break the scripts that open a transaction and create
// temporary tables inside it.
func splitStandaloneStatements(script string) []string {
	var chunks []string
	var current []string
	flush := func() {
		if len(current) == 0 {
			return
		}
		chunk := strings.Join(current, "\n")
		current = nil
		if strings.TrimSpace(chunk) != "" {
			chunks = append(chunks, chunk)
		}
	}
	for _, line := range strings.Split(script, "\n") {
		if vacuumPattern.MatchString(line) {
			flush()
			chunks = append(chunks, strings.TrimSpace(line))
			continue
		}
		current = append(current, line)
	}
	flush()
	return chunks
}

// copyFile streams a CSV into its staging table and returns the rows copied.
// The file is never read into memory: the ones this pipeline loads are measured
// in gigabytes.
func (e *executor) copyFile(ctx context.Context, file csvFile, path string) (int64, error) {
	handle, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("abrir %s: %w", file.FileName, err)
	}
	defer handle.Close()

	start := time.Now()
	e.logger.Info("importando", "archivo", file.FileName, "tabla", file.Table)

	lastReport := start
	source, err := newCSVSource(handle, file, func(rows int64) {
		// La lectura del reloj se hace cada tantas filas y no en cada una:
		// este callback corre millones de veces.
		if rows%50_000 != 0 {
			return
		}
		if time.Since(lastReport) < progressInterval {
			return
		}
		lastReport = time.Now()
		e.logger.Info(
			"importación en curso",
			"archivo", file.FileName,
			"filas", rows,
			"transcurrido", time.Since(start).Round(time.Second).String(),
		)
	})
	if err != nil {
		return 0, err
	}

	identifier := tableIdentifier(file.Table)
	copied, err := e.connection.CopyFrom(ctx, identifier, file.columnNames(), source)
	if err != nil {
		return 0, fmt.Errorf("copiar %s en %s: %w", file.FileName, file.Table, err)
	}
	e.logger.Info(
		"importación terminada",
		"archivo", file.FileName,
		"tabla", file.Table,
		"filas", copied,
		"duracion", time.Since(start).Round(time.Millisecond).String(),
	)
	return copied, nil
}

// tableIdentifier splits a qualified name so pgx quotes each part.
func tableIdentifier(table string) pgx.Identifier {
	return pgx.Identifier(strings.Split(table, "."))
}

// stripPsqlDirectives blanks the backslash commands the scripts carry for psql,
// such as \set ON_ERROR_STOP on. The server does not understand them, and it
// does not need to: an error in any statement of the string aborts the whole
// string, which is what ON_ERROR_STOP asks psql to do.
//
// The lines are blanked instead of removed so the line number PostgreSQL reports
// on an error is still the line number of the file on disk.
func stripPsqlDirectives(script string) string {
	lines := strings.Split(script, "\n")
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), `\`) {
			lines[index] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// WaitForDatabase opens the pool, retrying until the database accepts
// connections or the budget runs out. A container that has just been started is
// not ready yet, and the alternative to waiting is telling the user to run the
// same command again.
func WaitForDatabase(
	ctx context.Context,
	databaseURL string,
	budget time.Duration,
	retryInterval time.Duration,
	logger *slog.Logger,
) (*pgxpool.Pool, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	deadline := time.Now().Add(budget)
	var lastErr error
	for attempt := 1; ; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, retryInterval)
		pool, err := postgres.Open(attemptCtx, databaseURL)
		cancel()
		if err == nil {
			return pool, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf(
				"la base no respondió en %s: %w",
				budget.Round(time.Second), lastErr,
			)
		}
		logger.Info(
			"esperando a que la base acepte conexiones",
			"intento", attempt,
			"error", err.Error(),
		)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryInterval):
		}
	}
}
