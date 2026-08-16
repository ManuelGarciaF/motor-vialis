package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/httpapi"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.FromEnv()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	connectContext, cancelConnect := context.WithTimeout(context.Background(), 10*time.Second)
	database, err := postgres.Open(connectContext, cfg.DatabaseURL)
	cancelConnect()
	if err != nil {
		logger.Error("could not connect to PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	demandEstimator := demand.NewService(
		postgres.NewDemandRepository(database),
		config.SimulationAccessRadiusMeters,
		cfg.SimulationAccessibilityCalculator,
	)
	travelTimeEstimator := traveltime.NewService(
		postgres.NewTravelTimeRepository(database),
		cfg.SimulationTravelTimePolicy,
	)
	revenueEstimator := revenue.NewService(
		postgres.NewRevenueRepository(database),
		revenue.Policy{
			CaptureFactor:       cfg.SimulationRevenueCaptureFactor,
			RegisteredCardShare: cfg.SimulationRegisteredCardShare,
		},
	)
	service := simulation.NewService(demandEstimator, travelTimeEstimator, revenueEstimator)

	handler := httpapi.NewHandler(logger, service, service, cfg.SimulationTimeout)

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           handler.Routes(),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
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

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("could not gracefully stop HTTP server", "error", err)
		os.Exit(1)
	}
}
