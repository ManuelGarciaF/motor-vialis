package realtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
)

type fakeCapturer struct {
	call  func(context.Context) (bunching.Capture, error)
	calls int
}

func (f *fakeCapturer) Capture(ctx context.Context) (bunching.Capture, error) {
	f.calls++
	return f.call(ctx)
}

type fakeRepository struct {
	maintain                     func(context.Context, time.Time) (bunching.MaintenanceResult, error)
	store                        func(context.Context, bunching.Capture) (bunching.StoreResult, error)
	maintenanceCalls, storeCalls int
}

func (f *fakeRepository) MaintainPartitions(ctx context.Context, now time.Time) (bunching.MaintenanceResult, error) {
	f.maintenanceCalls++
	if f.maintain != nil {
		return f.maintain(ctx, now)
	}
	return bunching.MaintenanceResult{}, nil
}
func (f *fakeRepository) StoreCapture(ctx context.Context, c bunching.Capture) (bunching.StoreResult, error) {
	f.storeCalls++
	if f.store != nil {
		return f.store(ctx, c)
	}
	return bunching.StoreResult{Inserted: len(c.Positions)}, nil
}
func captureWithCount(n int) bunching.Capture {
	c := bunching.Capture{ReceivedAt: time.Now(), Positions: make([]bunching.Position, n), Stats: bunching.CaptureStats{TotalRows: n, ValidRows: n}}
	for i := range c.Positions {
		c.Positions[i].Fresh = true
		c.Positions[i].VehicleID = fmt.Sprint(i)
	}

	return c
}
func testWorker(t *testing.T, cap Capturer, repo bunching.Repository, logs *bytes.Buffer) *Worker {
	t.Helper()
	w, err := NewWorker(cap, repo, slog.New(slog.NewJSONHandler(logs, nil)), Options{Interval: time.Hour, DatabaseTimeout: time.Second, CoverageWarningRatio: .7})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestSequentialImmediateCaptureAndRecovery(t *testing.T) {
	var log bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var order []string
	cap := &fakeCapturer{}
	cap.call = func(ctx context.Context) (bunching.Capture, error) {
		order = append(order, "capture")
		if cap.calls == 2 {
			return bunching.Capture{}, errors.New("https://secret-provider/?secret=private")
		}
		return captureWithCount(100), nil
	}
	repo := &fakeRepository{}
	repo.store = func(ctx context.Context, c bunching.Capture) (bunching.StoreResult, error) {
		order = append(order, "store")
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("store has no timeout")
		}
		if repo.storeCalls == 1 {
			return bunching.StoreResult{}, errors.New("postgresql://password@private")
		}
		return bunching.StoreResult{Inserted: 100}, nil
	}
	w := testWorker(t, cap, repo, &log)
	w.wait = func(ctx context.Context, d time.Duration) error {
		order = append(order, "wait")
		if d != time.Hour {
			t.Fatalf("interval: %v", d)
		}
		if cap.calls == 3 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "capture,store,wait,capture,wait,capture,store,wait" {
		t.Fatal(got)
	}
	if repo.maintenanceCalls != 1 {
		t.Fatal("maintenance not performed at startup")
	}
	for _, event := range []string{"realtime_capture_failed", "realtime_store_failed", "realtime_capture_stored"} {
		if !strings.Contains(log.String(), event) {
			t.Fatal("missing " + event)
		}
	}

	if strings.Contains(log.String(), "private") || strings.Contains(log.String(), "password") {
		t.Fatal("unsafe errors logged")
	}
}

func TestCoverageWarningDoesNotDiscardOrResetBaseline(t *testing.T) {
	var log bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	counts := []int{100, 20, 20, 100}
	cap := &fakeCapturer{}
	cap.call = func(context.Context) (bunching.Capture, error) { return captureWithCount(counts[cap.calls-1]), nil }
	repo := &fakeRepository{}
	w := testWorker(t, cap, repo, &log)
	w.wait = func(ctx context.Context, _ time.Duration) error {
		if cap.calls == len(counts) {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if repo.storeCalls != 4 {
		t.Fatal("partial raw discarded")
	}
	if strings.Count(log.String(), `"coverage_warning":true`) != 2 {
		t.Fatal(log.String())
	}
}

func TestMaintenanceRetriesAfterUTCRollover(t *testing.T) {
	var log bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	now := time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC)
	cap := &fakeCapturer{}
	cap.call = func(context.Context) (bunching.Capture, error) { return captureWithCount(1), nil }
	repo := &fakeRepository{}
	repo.maintain = func(ctx context.Context, _ time.Time) (bunching.MaintenanceResult, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("maintenance has no timeout")
		}
		if repo.maintenanceCalls == 2 {
			return bunching.MaintenanceResult{}, errors.New("transient")
		}
		return bunching.MaintenanceResult{}, nil
	}
	w := testWorker(t, cap, repo, &log)
	w.options.Now = func() time.Time { return now }
	w.wait = func(ctx context.Context, _ time.Duration) error {
		now = now.Add(time.Second)
		if cap.calls == 3 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if repo.maintenanceCalls != 3 || repo.storeCalls != 3 {
		t.Fatalf("maintenance=%d store=%d", repo.maintenanceCalls, repo.storeCalls)
	}
}

func TestInitialMaintenanceFailureRetries(t *testing.T) {
	var log bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cap := &fakeCapturer{call: func(context.Context) (bunching.Capture, error) { return captureWithCount(1), nil }}
	repo := &fakeRepository{}
	repo.maintain = func(context.Context, time.Time) (bunching.MaintenanceResult, error) {
		if repo.maintenanceCalls == 1 {
			return bunching.MaintenanceResult{}, errors.New("private-secret")
		}
		return bunching.MaintenanceResult{}, nil
	}
	w := testWorker(t, cap, repo, &log)
	w.wait = func(ctx context.Context, _ time.Duration) error {
		if cap.calls == 2 {
			cancel()
			return ctx.Err()
		}
		return nil
	}
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if repo.maintenanceCalls != 2 || repo.storeCalls != 2 {
		t.Fatalf("maintenance=%d store=%d", repo.maintenanceCalls, repo.storeCalls)
	}
	if strings.Contains(log.String(), "private-secret") {
		t.Fatal("unsafe maintenance error logged")
	}
}

func TestCancellationDuringCapture(t *testing.T) {
	var log bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cap := &fakeCapturer{call: func(ctx context.Context) (bunching.Capture, error) {
		cancel()
		<-ctx.Done()
		return bunching.Capture{}, ctx.Err()
	}}
	repo := &fakeRepository{}
	w := testWorker(t, cap, repo, &log)
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if repo.storeCalls != 0 {
		t.Fatal("stored canceled capture")
	}
}

func TestCancellationDuringWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("shutdown waited for timer")
	}
}

func TestCancellationDuringStorage(t *testing.T) {
	var log bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cap := &fakeCapturer{call: func(context.Context) (bunching.Capture, error) { return captureWithCount(1), nil }}
	repo := &fakeRepository{store: func(ctx context.Context, _ bunching.Capture) (bunching.StoreResult, error) {
		cancel()
		<-ctx.Done()
		return bunching.StoreResult{}, ctx.Err()
	}}
	w := testWorker(t, cap, repo, &log)
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "realtime_store_failed") {
		t.Fatal("normal shutdown reported as failure")
	}
}
