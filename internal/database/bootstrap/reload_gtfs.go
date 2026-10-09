package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	scripts "github.com/ManuelGarciaF/vialis-motor/sql"
)

// ReloadGTFSOptions configures a GTFS-only reload of an existing database.
type ReloadGTFSOptions struct {
	// Directory contains the seven GTFS .txt files directly.
	Directory string
	Logger    *slog.Logger
}

// ReloadGTFS replaces the GTFS-derived model and rebuilds its downstream
// transfer tables without touching trips, H3 cells, the O-D matrix or tariffs.
func ReloadGTFS(ctx context.Context, pool *pgxpool.Pool, options ReloadGTFSOptions) error {
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if err := checkGTFSFiles(options.Directory); err != nil {
		return err
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("tomar una conexión del pool: %w", err)
	}
	defer connection.Release()

	runner := &executor{
		connection:    connection.Conn(),
		logger:        logger,
		dataDirectory: options.Directory,
	}
	if err := runner.checkGTFSReloadSchema(ctx); err != nil {
		return err
	}
	steps := []step{
		{
			name: "tablas de staging GTFS",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.CrearGTFSRaw)
			},
		},
		{
			name: "importación de los siete archivos GTFS",
			run: func(ctx context.Context, e *executor) error {
				for _, file := range gtfsFiles {
					if _, err := e.copyFile(ctx, file, filepath.Join(options.Directory, file.FileName)); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			name: "transformación GTFS",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.TransformarGTFS)
			},
		},
		{
			name: "conexiones entre recorridos",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.ConexionesRecorridos)
			},
		},
		{
			name: "ranking de combinaciones de líneas",
			run: func(ctx context.Context, e *executor) error {
				return e.runScript(ctx, scripts.CombinacionesLineas)
			},
		},
	}

	for index, definition := range steps {
		logger.Info("paso iniciado", "paso", index+1, "nombre", definition.name)
		started := time.Now()
		if err := definition.run(ctx, runner); err != nil {
			return fmt.Errorf("paso %d (%s): %w", index+1, definition.name, err)
		}
		logger.Info("paso terminado", "paso", index+1, "nombre", definition.name, "duracion", time.Since(started).Round(time.Millisecond))
	}
	return nil
}

func (e *executor) checkGTFSReloadSchema(ctx context.Context) error {
	const query = `
		SELECT COUNT(*) = 5
		FROM information_schema.columns
		WHERE table_schema = 'vialis'
		  AND (
		    (table_name = 'recorridos_paradas' AND column_name IN (
		      'tiempo_valle_hasta_siguiente_segundos',
		      'tiempo_tipico_hasta_siguiente_segundos',
		      'tiempo_pico_hasta_siguiente_segundos'
		    ))
		    OR
		    (table_name = 'combinaciones_lineas_flujos' AND column_name IN (
		      'nombre_origen', 'nombre_destino'
		    ))
		  )`
	var ready bool
	if err := e.connection.QueryRow(ctx, query).Scan(&ready); err != nil {
		return fmt.Errorf("verificar esquema para recarga GTFS: %w", err)
	}
	if !ready {
		return fmt.Errorf("la base requiere sql/recorridos/migrar_tiempos_tramos.sql y sql/migrar_combinaciones.sql antes de recargar GTFS")
	}
	return nil
}

func checkGTFSFiles(directory string) error {
	for _, definition := range gtfsFiles {
		path := filepath.Join(directory, definition.FileName)
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("abrir %s: %w", path, err)
		}
		_, parseErr := newCSVSource(file, definition, nil)
		closeErr := file.Close()
		if parseErr != nil {
			return parseErr
		}
		if closeErr != nil {
			return fmt.Errorf("cerrar %s: %w", path, closeErr)
		}
	}
	return nil
}
