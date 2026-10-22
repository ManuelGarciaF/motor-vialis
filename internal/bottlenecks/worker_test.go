package bottlenecks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type producerProcessorFunc func(context.Context, string) (ProducerProgress, error)

func (f producerProcessorFunc) ProcessNext(ctx context.Context, model string) (ProducerProgress, error) {
	return f(ctx, model)
}

func producerWorkerForTest(t *testing.T, process producerProcessorFunc, output *bytes.Buffer) *ProducerWorker {
	t.Helper()
	worker, err := NewProducerWorker(process, slog.New(slog.NewJSONHandler(output, nil)), ProducerWorkerOptions{
		Model: "experiment", Interval: time.Hour, CycleTimeout: time.Second, MaxCapturesPerCycle: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func TestProducerWorkerBoundsCapturesPerCycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	calls, waits := 0, 0
	worker := producerWorkerForTest(t, func(ctx context.Context, model string) (ProducerProgress, error) {
		if model != "experiment" || ctx.Err() != nil {
			t.Fatal("invalid processing context/model")
		}
		calls++
		return ProducerProgress{Processed: true, Checkpoint: int64(calls), Passages: 1}, nil
	}, &output)
	worker.wait = func(context.Context, time.Duration) error {
		waits++
		if calls != waits*2 {
			t.Fatal("capture budget was not honored")
		}
		if waits == 2 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if err := worker.Run(ctx); err != nil || calls != 4 || waits != 2 {
		t.Fatalf("calls=%d waits=%d err=%v", calls, waits, err)
	}
	if !strings.Contains(output.String(), `"captures":2`) {
		t.Fatal("committed source progress not reported")
	}
}

func TestProducerWorkerIdleWaitsWithoutInventingCaptures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	calls := 0
	worker := producerWorkerForTest(t, func(context.Context, string) (ProducerProgress, error) {
		calls++
		return ProducerProgress{Checkpoint: 42}, nil
	}, &output)
	worker.wait = func(_ context.Context, interval time.Duration) error {
		if interval != time.Hour || calls != 1 {
			t.Fatal("idle processing spun instead of waiting")
		}
		cancel()
		return ctx.Err()
	}
	if err := worker.Run(ctx); err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	if strings.Contains(output.String(), "captures_committed") {
		t.Fatal("idle result logged as a committed capture")
	}
}

func TestProducerWorkerRetriesTransientFailureWithoutLoggingPayload(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	calls, waits := 0, 0
	worker := producerWorkerForTest(t, func(context.Context, string) (ProducerProgress, error) {
		calls++
		switch calls {
		case 1:
			return ProducerProgress{}, errors.New("private-password SQL vehicle-value")
		case 2:
			return ProducerProgress{Processed: true, Checkpoint: 11}, nil
		default:
			return ProducerProgress{}, nil
		}
	}, &output)
	worker.wait = func(context.Context, time.Duration) error {
		waits++
		if waits == 2 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if err := worker.Run(ctx); err != nil || calls != 3 || waits != 2 {
		t.Fatalf("calls=%d waits=%d err=%v", calls, waits, err)
	}
	if strings.Contains(output.String(), "private-") || strings.Contains(output.String(), "vehicle-value") || !strings.Contains(output.String(), "operation_failed") {
		t.Fatal("unsafe or missing failure classification")
	}
}

func TestProducerWorkerPermanentFailuresStopOnlyProducer(t *testing.T) {
	for _, failure := range []error{ErrSourceExpired, ErrProducerSourceIncomplete, ErrProducerGeometryUnavailable, ErrModelChanged, ErrModelNotFound} {
		t.Run(failure.Error(), func(t *testing.T) {
			var output bytes.Buffer
			calls := 0
			worker := producerWorkerForTest(t, func(context.Context, string) (ProducerProgress, error) {
				calls++
				return ProducerProgress{}, fmt.Errorf("private-source-payload: %w", failure)
			}, &output)
			worker.wait = func(context.Context, time.Duration) error { t.Fatal("permanent failure retried"); return nil }
			if err := worker.Run(context.Background()); !errors.Is(err, failure) || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if !strings.Contains(output.String(), `"h0011_continues":true`) || strings.Contains(output.String(), "private-") {
				t.Fatal("isolation diagnosis missing or unsafe")
			}
		})
	}
}

func TestProducerWorkerCancellationJoinsInflightProcessing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	entered, cleanup := make(chan struct{}), make(chan struct{})
	worker := producerWorkerForTest(t, func(ctx context.Context, _ string) (ProducerProgress, error) {
		close(entered)
		<-ctx.Done()
		close(cleanup)
		return ProducerProgress{}, ctx.Err()
	}, &output)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("producer ignored cancellation")
	}
	select {
	case <-cleanup:
	default:
		t.Fatal("producer returned before processing cleaned up")
	}
}

func TestProducerWorkerCycleDeadlineIsTransient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	worker := producerWorkerForTest(t, func(ctx context.Context, _ string) (ProducerProgress, error) {
		<-ctx.Done()
		return ProducerProgress{}, ctx.Err()
	}, &output)
	worker.options.CycleTimeout = 5 * time.Millisecond
	worker.wait = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
	if err := worker.Run(ctx); err != nil || !strings.Contains(output.String(), "deadline_exceeded") {
		t.Fatalf("deadline not classified/retryable: %v %s", err, output.String())
	}
}

func TestProducerWorkerNeverOverlapsProcessing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	active, calls := 0, 0
	worker := producerWorkerForTest(t, func(context.Context, string) (ProducerProgress, error) {
		active++
		defer func() { active-- }()
		if active != 1 {
			t.Fatal("overlapping processing")
		}
		calls++
		return ProducerProgress{}, nil
	}, &output)
	worker.wait = func(context.Context, time.Duration) error {
		if active != 0 {
			t.Fatal("scheduler waits before processing finishes")
		}
		if calls == 3 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if err := worker.Run(ctx); err != nil || calls != 3 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestProducerWorkerRequiresExplicitOperationalLimits(t *testing.T) {
	process := producerProcessorFunc(func(context.Context, string) (ProducerProgress, error) { return ProducerProgress{}, nil })
	logger := slog.New(slog.DiscardHandler)
	base := ProducerWorkerOptions{Model: "m", Interval: time.Second, CycleTimeout: time.Second, MaxCapturesPerCycle: 1}
	for _, mutate := range []func(*ProducerWorkerOptions){
		func(o *ProducerWorkerOptions) { o.Model = "" },
		func(o *ProducerWorkerOptions) { o.Interval = 0 },
		func(o *ProducerWorkerOptions) { o.CycleTimeout = 0 },
		func(o *ProducerWorkerOptions) { o.MaxCapturesPerCycle = 0 },
	} {
		options := base
		mutate(&options)
		if _, err := NewProducerWorker(process, logger, options); err == nil {
			t.Fatal("invalid operational limit accepted")
		}
	}
	if _, err := NewProducerWorker(nil, logger, base); err == nil {
		t.Fatal("nil processor accepted")
	}
	if _, err := NewProducerWorker(process, nil, base); err == nil {
		t.Fatal("nil logger accepted")
	}
}
