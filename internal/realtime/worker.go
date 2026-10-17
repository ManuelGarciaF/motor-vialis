// Package realtime runs capture ingestion independently of simulation requests.
package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/ManuelGarciaF/vialis-motor/internal/transport/gcba"
	"github.com/jackc/pgx/v5/pgconn"
)

type Capturer interface {
	Capture(context.Context) (bunching.Capture, error)
}

type Options struct {
	Interval        time.Duration
	DatabaseTimeout time.Duration
	// CoverageWarningRatio is an operational warning, not an event detector.
	CoverageWarningRatio float64
	Now                  func() time.Time
}

type Worker struct {
	capturer   Capturer
	repository bunching.Repository
	logger     *slog.Logger
	options    Options
	wait       func(context.Context, time.Duration) error
}

func NewWorker(capturer Capturer, repository bunching.Repository, logger *slog.Logger, options Options) (*Worker, error) {
	if capturer == nil || repository == nil || logger == nil || options.Interval <= 0 || options.DatabaseTimeout <= 0 || options.CoverageWarningRatio <= 0 || options.CoverageWarningRatio >= 1 {
		return nil, fmt.Errorf("invalid realtime worker configuration")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Worker{capturer: capturer, repository: repository, logger: logger, options: options, wait: wait}, nil
}

// Run attempts maintenance before the first capture. Operational failures
// are logged and retried on the next cycle. No goroutine or ticker can
// overlap cycles: the next interval starts only after capture and storage end.
func (w *Worker) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	day := ""
	reference := 0
	var sequence uint64
	for ctx.Err() == nil {
		sequence++
		currentDay := w.options.Now().UTC().Format("2006-01-02")
		if currentDay != day {
			if err := w.maintain(ctx); err == nil {
				day = currentDay
			}
			if ctx.Err() != nil {
				return nil
			}
		}
		started := w.options.Now()
		capture, err := w.capturer.Capture(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			// Never log arbitrary error text: providers and DB drivers may embed URLs,
			// credentials, SQL values or vehicle identifiers.
			attributes := []any{"cycle", sequence, "duration_ms", w.options.Now().Sub(started).Milliseconds(), "error_class", ErrorClass(err)}
			var providerError *gcba.Error
			if errors.As(err, &providerError) && providerError.StatusCode != 0 {
				attributes = append(attributes, "http_status", providerError.StatusCode)
			}
			w.logger.Warn("realtime_capture_failed", attributes...)
		} else {
			freshIDs := make(map[string]struct{})
			for _, p := range capture.Positions {
				if p.Fresh {
					freshIDs[p.VehicleID] = struct{}{}
				}
			}
			fresh := len(freshIDs)
			previousReference := reference
			ratio := 1.0
			if reference > 0 {
				ratio = float64(fresh) / float64(reference)
			}
			partial := fresh == 0 || reference > 0 && ratio < w.options.CoverageWarningRatio
			capture.Partial = partial
			// A low capture cannot become the baseline for the following low capture.
			if !partial {
				reference = fresh
			}
			attrs := []any{"cycle", sequence, "received_at", capture.ReceivedAt, "rows_total", capture.Stats.TotalRows, "rows_valid", capture.Stats.ValidRows, "rows_invalid", capture.Stats.InvalidRows, "rows_duplicate", capture.Stats.DuplicateRows, "rows_stale", capture.Stats.StaleRows, "fresh_vehicles", fresh, "coverage_reference_vehicles", previousReference, "coverage_ratio", ratio, "coverage_warning", partial}
			dbContext, cancel := context.WithTimeout(ctx, w.options.DatabaseTimeout)
			result, storeErr := w.repository.StoreCapture(dbContext, capture)
			cancel()
			if ctx.Err() != nil {
				return nil
			}
			attrs = append(attrs, "duration_ms", w.options.Now().Sub(started).Milliseconds())
			if storeErr != nil {
				w.logger.Warn("realtime_store_failed", append(attrs, "error_class", ErrorClass(storeErr))...)
			} else {
				attrs = append(attrs, "capture_id", result.CaptureID, "rows_inserted", result.Inserted, "rows_unmapped", result.Unmapped, "rows_outside_retention", result.OutsideRetention, "bunching_processed", result.BunchingProcessed, "bunching_skipped", result.BunchingSkipped, "events_opened", result.EventsOpened, "events_updated", result.EventsUpdated, "events_closed", result.EventsClosed)
				if partial {
					w.logger.Warn("realtime_capture_stored", attrs...)
				} else {
					w.logger.Info("realtime_capture_stored", attrs...)
				}
			}
		}
		if err := w.wait(ctx, w.options.Interval); err != nil {
			return nil
		}
	}
	return nil
}

func (w *Worker) maintain(ctx context.Context) error {
	dbContext, cancel := context.WithTimeout(ctx, w.options.DatabaseTimeout)
	defer cancel()
	result, err := w.repository.MaintainPartitions(dbContext, w.options.Now())
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Warn("realtime_maintenance_failed", "error_class", ErrorClass(err))
		}
		return err
	}
	w.logger.Info("realtime_maintenance_completed", "partitions_created", len(result.Created), "partitions_dropped", len(result.Dropped))
	return nil
}

func wait(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ErrorClass is safe for logs, unlike an arbitrary driver's Error() string.
func ErrorClass(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	var providerError *gcba.Error
	if errors.As(err, &providerError) {
		return "gcba_" + string(providerError.Code)
	}
	var dbError *pgconn.PgError
	if errors.As(err, &dbError) {
		return "postgres_" + dbError.Code
	}
	return "operation_failed"
}
