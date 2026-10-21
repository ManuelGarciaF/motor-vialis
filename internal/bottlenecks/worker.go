package bottlenecks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// ProducerProcessor advances one actual source capture atomically. An idle
// result must not advance receipt state or increment vehicle absences.
type ProducerProcessor interface {
	ProcessNext(context.Context, string) (ProducerProgress, error)
}

// ProducerWorkerOptions bounds operational work, not GPS continuity or sequence
// lifetime. Interval must be chosen explicitly while incremental cost is measured.
type ProducerWorkerOptions struct {
	Model               string
	Interval            time.Duration
	CycleTimeout        time.Duration
	MaxCapturesPerCycle int
}

type ProducerWorker struct {
	processor ProducerProcessor
	logger    *slog.Logger
	options   ProducerWorkerOptions
	wait      func(context.Context, time.Duration) error
}

func NewProducerWorker(processor ProducerProcessor, logger *slog.Logger, options ProducerWorkerOptions) (*ProducerWorker, error) {
	if processor == nil || logger == nil || options.Model == "" || options.Interval <= 0 || options.CycleTimeout <= 0 || options.MaxCapturesPerCycle <= 0 {
		return nil, fmt.Errorf("invalid producer worker configuration")
	}
	return &ProducerWorker{processor: processor, logger: logger, options: options, wait: waitProducerInterval}, nil
}

type producerCycle struct {
	captures, passages, incomplete int
	checkpoint                     int64
}

// Run owns one serial processing loop. Permanent source/model failures stop
// this loop only; the process coordinator keeps H0011 ingestion independent.
// Completed captures remain committed if a later capture exhausts the budget.
func (w *ProducerWorker) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		cycle, err := w.runCycle(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if cycle.captures > 0 {
			w.logger.Info("h0012_captures_committed", "captures", cycle.captures, "checkpoint", cycle.checkpoint,
				"passages", cycle.passages, "incomplete", cycle.incomplete)
		}
		if err != nil {
			code, permanent := producerWorkerError(err)
			if permanent {
				w.logger.Error("h0012_processing_stopped", "error_class", code, "h0011_continues", true)
				return err
			}
			w.logger.Warn("h0012_cycle_failed", "error_class", code)
		}
		if err := w.wait(ctx, w.options.Interval); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
	return nil
}

func (w *ProducerWorker) runCycle(ctx context.Context) (producerCycle, error) {
	cycleCtx, cancel := context.WithTimeout(ctx, w.options.CycleTimeout)
	defer cancel()

	var cycle producerCycle
	for i := 0; i < w.options.MaxCapturesPerCycle; i++ {
		if err := cycleCtx.Err(); err != nil {
			return cycle, err
		}
		progress, err := w.processor.ProcessNext(cycleCtx, w.options.Model)
		if err != nil {
			return cycle, err
		}
		if !progress.Processed {
			return cycle, nil
		}
		cycle.captures++
		cycle.passages += progress.Passages
		cycle.incomplete += progress.Incomplete
		cycle.checkpoint = progress.Checkpoint
	}
	return cycle, nil
}

func producerWorkerError(err error) (string, bool) {
	for _, failure := range []struct {
		err  error
		code string
	}{
		{ErrSourceExpired, "source_expired"},
		{ErrProducerSourceIncomplete, "source_incomplete"},
		{ErrProducerGeometryUnavailable, "geometry_unavailable"},
		{ErrProducerModeConflict, "processing_mode_conflict"},
		{ErrModelChanged, "model_changed"},
		{ErrModelNotFound, "model_not_found"},
	} {
		if errors.Is(err, failure.err) {
			return failure.code, true
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded", false
	}
	if errors.Is(err, context.Canceled) {
		return "canceled", false
	}
	return "operation_failed", false
}

func waitProducerInterval(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
