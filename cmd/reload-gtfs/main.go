// Command reload-gtfs replaces GTFS-derived data in an existing Vialis database.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/bootstrap"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
)

func main() {
	cfg := config.FromEnv()
	databaseURL := flag.String("database-url", cfg.DatabaseURL, "PostgreSQL connection URL")
	dataDirectory := flag.String("data-dir", "colectivos-gtfs", "directorio con los siete archivos GTFS .txt")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := postgres.Open(ctx, *databaseURL)
	if err != nil {
		logger.Error("no se pudo conectar a PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	if err := bootstrap.ReloadGTFS(ctx, database, bootstrap.ReloadGTFSOptions{
		Directory: *dataDirectory,
		Logger:    logger,
	}); err != nil {
		logger.Error("no se pudo recargar GTFS", "error", err)
		os.Exit(1)
	}
}
