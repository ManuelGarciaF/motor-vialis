package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/realtime"
	"github.com/ManuelGarciaF/vialis-motor/internal/transport/gcba"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], logger, os.Stderr); err != nil {
		logStartupError(logger, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, logger *slog.Logger, output io.Writer) error {
	flags := flag.NewFlagSet("realtime-worker", flag.ContinueOnError)
	flags.SetOutput(output)
	interval := flags.Duration("interval", config.RealtimePollInterval, "wait after each completed cycle (e.g. 1m)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *interval <= 0 {
		return fmt.Errorf("invalid realtime-worker arguments")
	}
	cfg := config.FromEnv()
	if cfg.GCBATransportClientID == "" || cfg.GCBATransportClientSecret == "" {
		return &startupError{code: "credentials_missing", hint: "Export GCBA_TRANSPORT_CLIENT_ID and GCBA_TRANSPORT_CLIENT_SECRET; the worker does not load .env automatically"}
	}
	// Fail before opening the database when required credentials are absent.
	client, err := gcba.NewClient(gcba.Options{
		ClientID: cfg.GCBATransportClientID, ClientSecret: cfg.GCBATransportClientSecret,
		RequestTimeout: config.GCBARequestTimeout, MaximumAge: config.GCBAMaximumPositionAge,
		MaximumFutureSkew: config.GCBAMaximumFutureSkew, MaximumBodyBytes: config.GCBAMaximumBodyBytes,
		MaximumAttempts: config.GCBAMaximumAttempts, InitialBackoff: config.GCBAInitialBackoff, MaximumBackoff: config.GCBAMaximumBackoff,
	})
	if err != nil {
		return &startupError{code: "gcba_configuration_failed", hint: "Check GCBA client configuration"}
	}
	if os.Getenv("DATABASE_URL") == "" {
		logger.Warn("realtime_database_default_used", "hint", "DATABASE_URL is not set; using localhost:5432/vialis")
	}
	connectContext, cancel := context.WithTimeout(ctx, config.DatabaseConnectTimeout)
	pool, err := postgres.Open(connectContext, cfg.DatabaseURL)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return &startupError{code: "database_connection_failed", hint: "Check DATABASE_URL, PostgreSQL availability, credentials and network reachability"}
	}
	defer pool.Close()
	checkContext, cancelCheck := context.WithTimeout(ctx, config.DatabaseConnectTimeout)
	ready, err := postgres.RealtimeSchemaReady(checkContext, pool)
	cancelCheck()
	if err != nil || !ready {
		if ctx.Err() != nil {
			return nil
		}
		logger.Error("realtime_schema_not_ready")
		return &startupError{code: "schema_check_failed", hint: "Check database permissions and apply sql/recorridos/migrar_realtime.sql and migrar_aglutinaciones.sql"}
	}
	repository, err := postgres.NewRealtimeRepository(pool, config.RealtimeRawRetentionDays)
	if err != nil {
		return &startupError{code: "repository_configuration_failed", hint: "Check realtime repository configuration"}
	}
	if err = repository.EnableBunching(config.BunchingPolicy()); err != nil {
		return &startupError{code: "bunching_configuration_failed", hint: "Check bunching policy"}
	}
	worker, err := realtime.NewWorker(client, repository, logger, realtime.Options{Interval: *interval, DatabaseTimeout: config.RealtimeDatabaseTimeout, CoverageWarningRatio: config.RealtimeCoverageWarningRatio})
	if err != nil {
		return &startupError{code: "worker_configuration_failed", hint: "Check worker interval and timeouts"}
	}
	logger.Info("realtime_worker_started", "interval_ms", interval.Milliseconds(), "raw_retention_days", config.RealtimeRawRetentionDays, "endpoint", "vehiclePositionsSimple")
	err = worker.Run(ctx)
	if err == nil {
		logger.Info("realtime_worker_stopped")
	}
	return err
}

// Startup diagnostics are authored hints, never arbitrary driver error strings.
type startupError struct {
	code string
	hint string
}

func (err *startupError) Error() string { return err.code + ": " + err.hint }

func logStartupError(logger *slog.Logger, err error) {
	var startup *startupError
	if errors.As(err, &startup) {
		logger.Error("realtime_worker_start_failed", "error_class", startup.code, "hint", startup.hint)
		return
	}
	logger.Error("realtime_worker_start_failed", "error_class", realtime.ErrorClass(err))
}
