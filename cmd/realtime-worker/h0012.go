package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

type h0012Store interface {
	bottlenecks.ProducerProcessor
	ProducerSchemaReady(context.Context) (bool, error)
	LoadProducerStates(context.Context, string) (bottlenecks.ProcessingRun, []bottlenecks.ProducerState, error)
	Activate(context.Context, bottlenecks.PersistenceModel) (bottlenecks.ProcessingRun, error)
}

// prepareH0012 does no database work when disabled. Its private bounded pool
// prevents backlog processing from consuming the ingestion pool's connections.
func prepareH0012(ctx context.Context, databaseURL string, options h0012Options, logger *slog.Logger) (func(context.Context) error, func(), error) {
	if options.model == "" {
		return nil, func() {}, nil
	}
	poolConfig, err := h0012PoolConfig(databaseURL)
	if err != nil {
		return nil, nil, &startupError{code: "h0012_database_configuration_failed", hint: "Check DATABASE_URL"}
	}
	connectCtx, cancelConnect := context.WithTimeout(ctx, config.DatabaseConnectTimeout)
	pool, err := pgxpool.NewWithConfig(connectCtx, poolConfig)
	if err == nil {
		err = pool.Ping(connectCtx)
	}
	cancelConnect()
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		return nil, nil, &startupError{code: "h0012_database_connection_failed", hint: "Check PostgreSQL connectivity for the independent H0012 pool"}
	}

	store, err := postgres.NewBottlenecksStore(pool)
	if err == nil {
		checkCtx, cancelCheck := context.WithTimeout(ctx, config.RealtimeDatabaseTimeout)
		err = initializeH0012(checkCtx, store, options)
		cancelCheck()
	}
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	worker, err := bottlenecks.NewProducerWorker(store, logger, bottlenecks.ProducerWorkerOptions{
		Model: options.model, Interval: options.interval, CycleTimeout: h0012CycleTimeout,
		MaxCapturesPerCycle: h0012MaxCapturesPerCycle,
	})
	if err != nil {
		pool.Close()
		return nil, nil, &startupError{code: "h0012_worker_configuration_failed", hint: "Check H0012 scheduling options"}
	}
	logger.Info("h0012_worker_ready", "activation_requested", options.activate, "interval_ms", options.interval.Milliseconds(),
		"cycle_timeout_ms", h0012CycleTimeout.Milliseconds(), "max_captures_per_cycle", h0012MaxCapturesPerCycle,
		"pool_max_connections", h0012PoolMaxConnections)
	return worker.Run, pool.Close, nil
}

func h0012PoolConfig(databaseURL string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = h0012PoolMaxConnections
	cfg.MinConns = 0
	return cfg, nil
}

// Initialization is explicit and finishes before H0011 can capture new rows.
// Resume validates persisted policy but never reactivates or resets a model.
func initializeH0012(ctx context.Context, store h0012Store, options h0012Options) error {
	ready, err := store.ProducerSchemaReady(ctx)
	if err != nil || !ready {
		return &startupError{code: "h0012_schema_not_ready", hint: "Apply sql/bottlenecks/migrar_persistencia.sql and migrar_productor.sql explicitly"}
	}

	var run bottlenecks.ProcessingRun
	if options.activate {
		_, err = store.Activate(ctx, options.persistenceModel())
	}
	if err == nil {
		run, _, err = store.LoadProducerStates(ctx, options.model)
	}
	if err != nil {
		code := "h0012_model_start_failed"
		switch {
		case errors.Is(err, bottlenecks.ErrModelNotFound):
			code = "h0012_model_not_found"
		case errors.Is(err, bottlenecks.ErrModelChanged):
			code = "h0012_model_changed"
		case errors.Is(err, bottlenecks.ErrProducerModeConflict):
			code = "h0012_model_mode_conflict"
		}
		return &startupError{code: code, hint: "Check the activated model, its configuration and processing mode; activation is explicit"}
	}
	model := run.Model
	if model.Validate() != nil || model.Name != options.model || model.MeasurementVersion != h0012MeasurementVersion ||
		model.MatcherVersion != h0012MatcherVersion || model.TimeZone != config.BottlenecksAnalysisTimeZone ||
		model.RawRetentionDays != config.RealtimeRawRetentionDays || model.ClosureCaptures != bottlenecks.MissingCaptureLimit ||
		(options.activate && model != options.persistenceModel()) {
		return &startupError{code: "h0012_model_incompatible", hint: "The persisted model must match this producer's versions, timezone, retention and closure policy"}
	}
	return nil
}

// A stopped producer is isolated from ingestion. Ingestion termination or
// process cancellation cancels and joins the producer before any pool closes.
func runWorkerLoops(ctx context.Context, ingest func(context.Context) error, produce func(context.Context) error) error {
	if produce == nil {
		return ingest(ctx)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	producerDone := make(chan error, 1)
	go func() { producerDone <- produce(ctx) }()

	err := ingest(ctx)
	cancel()
	<-producerDone
	return err
}
