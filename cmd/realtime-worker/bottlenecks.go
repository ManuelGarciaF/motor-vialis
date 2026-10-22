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

type bottlenecksStore interface {
	bottlenecks.ProducerProcessor
	ProducerSchemaReady(context.Context) (bool, error)
	LoadProducerStates(context.Context, string) (bottlenecks.ProcessingRun, []bottlenecks.ProducerState, error)
	Activate(context.Context, bottlenecks.PersistenceModel) (bottlenecks.ProcessingRun, error)
}

// prepareBottlenecks always initializes the producer. Its private bounded pool
// prevents backlog processing from consuming the ingestion pool's connections.
func prepareBottlenecks(ctx context.Context, databaseURL string, logger *slog.Logger) (func(context.Context) error, func(), error) {
	poolConfig, err := bottlenecksPoolConfig(databaseURL)
	if err != nil {
		return nil, nil, &startupError{code: "bottlenecks_database_configuration_failed", hint: "Check DATABASE_URL"}
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
		return nil, nil, &startupError{code: "bottlenecks_database_connection_failed", hint: "Check PostgreSQL connectivity for the independent Bottlenecks pool"}
	}

	store, err := postgres.NewBottlenecksStore(pool)
	if err == nil {
		checkCtx, cancelCheck := context.WithTimeout(ctx, config.RealtimeDatabaseTimeout)
		err = initializeBottlenecks(checkCtx, store)
		cancelCheck()
	}
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	worker, err := bottlenecks.NewProducerWorker(store, logger, bottlenecks.ProducerWorkerOptions{
		Model: config.BottlenecksModel().Name, Interval: config.BottlenecksInterval, CycleTimeout: config.BottlenecksCycleTimeout,
		MaxCapturesPerCycle: config.BottlenecksMaxCapturesPerCycle,
	})
	if err != nil {
		pool.Close()
		return nil, nil, &startupError{code: "bottlenecks_worker_configuration_failed", hint: "Check Bottlenecks scheduling options"}
	}
	logger.Info("bottlenecks_worker_ready", "interval_ms", config.BottlenecksInterval.Milliseconds(),
		"cycle_timeout_ms", config.BottlenecksCycleTimeout.Milliseconds(), "max_captures_per_cycle", config.BottlenecksMaxCapturesPerCycle,
		"pool_max_connections", config.BottlenecksPoolMaxConnections)
	return worker.Run, pool.Close, nil
}

func bottlenecksPoolConfig(databaseURL string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = "vialis-bottlenecks"
	cfg.MaxConns = config.BottlenecksPoolMaxConnections
	cfg.MinConns = 0
	return cfg, nil
}

// Initialization finishes before ingestion starts. Only a missing configuration
// is initialized; resume never resets its original activation or checkpoint.
func initializeBottlenecks(ctx context.Context, store bottlenecksStore) error {
	ready, err := store.ProducerSchemaReady(ctx)
	if err != nil || !ready {
		return &startupError{code: "bottlenecks_schema_not_ready", hint: "Check the bottlenecks tables defined in sql/ddl.sql; existing databases require explicit schema administration"}
	}

	model := config.BottlenecksModel()
	run, _, err := store.LoadProducerStates(ctx, model.Name)
	if errors.Is(err, bottlenecks.ErrModelNotFound) {
		_, err = store.Activate(ctx, model)
		if err == nil {
			run, _, err = store.LoadProducerStates(ctx, model.Name)
		}
	}
	if err != nil {
		code := "bottlenecks_model_start_failed"
		switch {
		case errors.Is(err, bottlenecks.ErrModelNotFound):
			code = "bottlenecks_model_not_found"
		case errors.Is(err, bottlenecks.ErrModelChanged):
			code = "bottlenecks_model_changed"
		}
		return &startupError{code: code, hint: "Check the operational measurement configuration and database permissions; existing checkpoints are never reset"}
	}
	if run.Model.Validate() != nil || run.Model != model {
		return &startupError{code: "bottlenecks_model_incompatible", hint: "The persisted measurement configuration must match internal/config/parameters.go; do not reset existing checkpoints"}
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
