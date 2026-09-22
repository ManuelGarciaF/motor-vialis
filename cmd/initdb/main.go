// Command initdb builds the Vialis database from scratch: schema, extensions,
// tables, the OSM road graph, the GTFS feed, the trip survey and every
// transformation between them.
//
//	docker compose up -d --build
//	go run ./cmd/initdb
//
// It is the companion of docker-compose.yml, so its default connection points at
// the container that file starts, on port 5433. DATABASE_URL overrides it.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/database/bootstrap"
)

const (
	// defaultDatabaseURL is the database started by docker-compose.yml. It is
	// not config.DefaultDatabaseURL because that one is the port the engine
	// looks at by default (5432); the container publishes 5433 so it does not
	// collide with a PostgreSQL already installed on the machine.
	defaultDatabaseURL = "postgresql://postgres:postgres@localhost:5433/vialis"

	// waitBudget bounds the wait for a container that is still starting.
	waitBudget = 2 * time.Minute

	// waitInterval is how long each connection attempt gets, and how long the
	// next one waits.
	waitInterval = 2 * time.Second
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	reset := flag.Bool(
		"reset",
		false,
		"borra el esquema vialis y lo vuelve a crear; destruye los datos cargados",
	)
	dataDirectory := flag.String(
		"data-dir",
		".",
		"directorio con viajes_BAdata_20241016.csv, calles.osm y colectivos-gtfs/",
	)
	flag.Parse()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = defaultDatabaseURL
	}

	// Una carga completa tarda varios minutos, así que no hay presupuesto total:
	// lo que corta la ejecución es Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := bootstrap.WaitForDatabase(ctx, databaseURL, waitBudget, waitInterval, logger)
	if err != nil {
		logger.Error("no se pudo conectar a PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	err = bootstrap.Run(ctx, database, bootstrap.Options{
		DataDirectory: *dataDirectory,
		StreetsFile:   filepath.Join(*dataDirectory, "calles.osm"),
		DatabaseURL:   databaseURL,
		Reset:         *reset,
		Logger:        logger,
	})
	if err != nil {
		logger.Error("no se pudo inicializar la base de datos", "error", err)
		os.Exit(1)
	}
}
