package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ManuelGarciaF/vialis-motor/internal/app"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/httpapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.FromEnv()

	connectContext, cancelConnect := context.WithTimeout(
		context.Background(),
		config.DatabaseConnectTimeout,
	)
	database, err := postgres.Open(connectContext, cfg.DatabaseURL)
	cancelConnect()
	if err != nil {
		logger.Error("could not connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	service := app.NewSimulationService(database)
	linesService := app.NewLinesService(database)

	handler := httpapi.NewHandler(
		logger,
		service,
		service,
		linesService,
		config.SimulationTimeout,
	)

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: config.ReadHeaderTimeout,
		ReadTimeout:       config.ReadTimeout,
		WriteTimeout:      config.WriteTimeout,
		IdleTimeout:       config.IdleTimeout,
	}

	shutdownSignal, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serverError := make(chan error, 1)
	go func() {
		logger.Info("HTTP server started", "address", cfg.HTTPAddress)
		serverError <- server.ListenAndServe()
	}()

	select {
	case err = <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	case <-shutdownSignal.Done():
		logger.Info("shutting down HTTP server")
	}

	shutdownContext, cancel := context.WithTimeout(
		context.Background(),
		config.DatabaseConnectTimeout,
	)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("could not gracefully stop HTTP server", "error", err)
		os.Exit(1)
	}
}
