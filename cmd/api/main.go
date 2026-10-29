package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/app"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/httpapi"
)

func main() {
	cfg := config.FromEnv()
	demo := flag.Bool("demo", cfg.DemoMode, "freeze worker API time at the latest analyzed capture (defaults to DEMO_MODE; stop worker first)")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	trafficClient, err := app.NewDetourTrafficProvider(cfg.TomTomAPIKey, logger, *demo)
	if err != nil {
		logger.Error("could not configure TomTom traffic", "error", err)
		os.Exit(1)
	}

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

	var demoAt time.Time
	if *demo {
		ctx, cancel := context.WithTimeout(context.Background(), config.DatabaseConnectTimeout)
		demoAt, err = postgres.NewBunchingRepository(database).LatestAnalyzedCaptureAt(ctx)
		cancel()
		if err != nil {
			logger.Error("demo requires a retained analyzed capture", "error", err)
			os.Exit(1)
		}
		logger.Warn("demo mode: worker API clock frozen; stop worker and preserve database snapshot", "demo_time", demoAt)
	}

	service := app.NewSimulationService(database)
	detourService, err := app.NewDetourService(database, trafficClient, service)
	if err != nil {
		logger.Error("could not configure detour service", "error", err)
		os.Exit(1)
	}
	detourService.WithDemoTrafficFallback(*demo)
	segmentService, err := app.NewSegmentService(database)
	if err != nil {
		logger.Error("could not configure segment routing", "error", err)
		os.Exit(1)
	}
	linesService := app.NewLinesService(database)
	transfersService := app.NewCombinacionesService(database)
	bunchingService, err := app.NewBunchingService(database)
	if err != nil {
		logger.Error("could not configure bunching")
		os.Exit(1)
	}
	if *demo {
		bunchingService.WithFixedTime(demoAt)
	}

	bottlenecksService, err := app.NewBottlenecksService(database)
	if err != nil {
		logger.Error("could not configure bottlenecks")
		os.Exit(1)
	}

	handler := httpapi.NewHandler(
		logger,
		service,
		service,
		detourService,
		linesService,
		transfersService,
		config.SimulationTimeout,
		bunchingService,
	).WithBottlenecks(bottlenecksService).WithSegmentRouter(segmentService)
	if *demo {
		handler.WithDemoTime(demoAt)
	}

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
