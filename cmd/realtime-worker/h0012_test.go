package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
)

func explicitH0012Arguments() []string {
	return []string{"-h0012-model=experiment", "-h0012-interval=2m", "-h0012-activate", "-h0012-segment-meters=500", "-h0012-max-gap=180s", "-h0012-max-distance-meters=100", "-h0012-max-speed-kph=100"}
}

func TestH0012ArgumentsRequireExplicitActivationPolicyAndCadence(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
	}{
		{"model without cadence", []string{"-h0012-model=m"}},
		{"zero cadence", []string{"-h0012-model=m", "-h0012-interval=0"}},
		{"blank model", []string{"-h0012-model= ", "-h0012-interval=1m"}},
		{"orphan cadence", []string{"-h0012-interval=1m"}},
		{"orphan activation", []string{"-h0012-activate"}},
		{"orphan policy", []string{"-h0012-max-gap=180s"}},
		{"resume policy override", []string{"-h0012-model=m", "-h0012-interval=1m", "-h0012-segment-meters=500"}},
		{"activation missing policy", []string{"-h0012-model=m", "-h0012-interval=1m", "-h0012-activate"}},
		{"negative policy", append(explicitH0012Arguments(), "-h0012-max-distance-meters=-1")},
		{"nonfinite policy", append(explicitH0012Arguments(), "-h0012-segment-meters=NaN")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseWorkerOptions(test.args, io.Discard); err == nil {
				t.Fatal("invalid H0012 options accepted")
			}
		})
	}
	for _, name := range []string{"h0012-segment-meters", "h0012-max-gap", "h0012-max-distance-meters", "h0012-max-speed-kph"} {
		t.Run("explicit "+name, func(t *testing.T) {
			var args []string
			for _, arg := range explicitH0012Arguments() {
				if !strings.HasPrefix(arg, "-"+name+"=") {
					args = append(args, arg)
				}
			}
			if _, err := parseWorkerOptions(args, io.Discard); err == nil {
				t.Fatal("omitted explicit policy accepted")
			}
		})
	}
}

func TestH0012ArgumentModes(t *testing.T) {
	legacy, err := parseWorkerOptions(nil, io.Discard)
	if err != nil || legacy.h0012.model != "" || legacy.interval != config.RealtimePollInterval {
		t.Fatalf("legacy invocation changed: %+v %v", legacy, err)
	}
	resume, err := parseWorkerOptions([]string{"-h0012-model=experiment", "-h0012-interval=2m"}, io.Discard)
	if err != nil || resume.h0012.activate || resume.h0012.interval != 2*time.Minute {
		t.Fatalf("resume: %+v %v", resume, err)
	}
	activated, err := parseWorkerOptions(explicitH0012Arguments(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	model := activated.h0012.persistenceModel()
	if model.Validate() != nil || model.NominalMeters != 500 || model.Policy.MaximumGap != 180*time.Second || model.Policy.MaximumDistanceMeters != 100 || model.Policy.MaximumSpeedKPH != 100 || model.RawRetentionDays != config.RealtimeRawRetentionDays || model.ClosureCaptures != 4 {
		t.Fatalf("explicit model corrupted: %+v", model)
	}
	args := append(explicitH0012Arguments(), "-h0012-max-distance-meters=0")
	if _, err = parseWorkerOptions(args, io.Discard); err != nil {
		t.Fatal("explicit zero radius should be allowed by MeasurementPolicy")
	}
}

type fakeH0012Store struct {
	ready                             bool
	model                             bottlenecks.PersistenceModel
	calls                             []string
	schemaErr, loadErr, activationErr error
}

func (s *fakeH0012Store) ProducerSchemaReady(context.Context) (bool, error) {
	s.calls = append(s.calls, "schema")
	return s.ready, s.schemaErr
}
func (s *fakeH0012Store) Activate(_ context.Context, model bottlenecks.PersistenceModel) (bottlenecks.ProcessingRun, error) {
	s.calls = append(s.calls, "activate")
	if s.activationErr == nil {
		s.model = model
	}
	return bottlenecks.ProcessingRun{Model: s.model}, s.activationErr
}
func (s *fakeH0012Store) LoadProducerStates(context.Context, string) (bottlenecks.ProcessingRun, []bottlenecks.ProducerState, error) {
	s.calls = append(s.calls, "load")
	return bottlenecks.ProcessingRun{Model: s.model}, nil, s.loadErr
}
func (s *fakeH0012Store) ProcessNext(context.Context, string) (bottlenecks.ProducerProgress, error) {
	panic("initialization must not process a capture")
}

func TestH0012InitializationNeverActivatesOnResume(t *testing.T) {
	parsed, err := parseWorkerOptions(explicitH0012Arguments(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	options := parsed.h0012
	store := &fakeH0012Store{ready: true, model: options.persistenceModel()}
	options.activate = false
	if err = initializeH0012(context.Background(), store, options); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.calls, []string{"schema", "load"}) {
		t.Fatalf("resume activated or processed: %v", store.calls)
	}
	store.calls = nil
	options.activate = true
	if err = initializeH0012(context.Background(), store, options); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.calls, []string{"schema", "activate", "load"}) {
		t.Fatalf("activation/startup validation order: %v", store.calls)
	}
}

func TestH0012StartupRejectsSchemaModeAndModelBeforeProcessing(t *testing.T) {
	parsed, err := parseWorkerOptions(explicitH0012Arguments(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	options := parsed.h0012
	options.activate = false
	for _, test := range []struct {
		name string
		edit func(*fakeH0012Store)
	}{
		{"missing schema", func(s *fakeH0012Store) { s.ready = false }},
		{"schema error", func(s *fakeH0012Store) { s.schemaErr = errors.New("private-schema-detail") }},
		{"missing model", func(s *fakeH0012Store) { s.loadErr = bottlenecks.ErrModelNotFound }},
		{"legacy mode", func(s *fakeH0012Store) { s.loadErr = bottlenecks.ErrProducerModeConflict }},
		{"driver error", func(s *fakeH0012Store) { s.loadErr = errors.New("private-database-url") }},
		{"name", func(s *fakeH0012Store) { s.model.Name = "other" }},
		{"measurement version", func(s *fakeH0012Store) { s.model.MeasurementVersion = "other" }},
		{"matcher version", func(s *fakeH0012Store) { s.model.MatcherVersion = "other" }},
		{"retention", func(s *fakeH0012Store) { s.model.RawRetentionDays = 3 }},
		{"closure", func(s *fakeH0012Store) { s.model.ClosureCaptures = 3 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeH0012Store{ready: true, model: options.persistenceModel()}
			test.edit(store)
			err := initializeH0012(context.Background(), store, options)
			var startup *startupError
			if !errors.As(err, &startup) || strings.Contains(err.Error(), "private-") {
				t.Fatalf("missing/unsafe startup failure: %v", err)
			}
			for _, call := range store.calls {
				if call == "activate" {
					t.Fatal("startup resume activated a model")
				}
			}
		})
	}
	store := &fakeH0012Store{ready: true, activationErr: bottlenecks.ErrModelChanged}
	options.activate = true
	if err = initializeH0012(context.Background(), store, options); err == nil || !reflect.DeepEqual(store.calls, []string{"schema", "activate"}) {
		t.Fatalf("incompatible activation was not stopped: %v %v", store.calls, err)
	}
}

func TestH0012DisabledDoesNotEvenParseDatabaseURL(t *testing.T) {
	run, closePool, err := prepareH0012(context.Background(), "invalid-private-url", h0012Options{}, slog.New(slog.DiscardHandler))
	if err != nil || run != nil || closePool == nil {
		t.Fatalf("disabled H0012 touched database: %v", err)
	}
	closePool()
}

func TestH0012PoolIsBoundedIndependently(t *testing.T) {
	cfg, err := h0012PoolConfig("postgresql://localhost/vialis?pool_max_conns=20&pool_min_conns=10")
	if err != nil || cfg.MaxConns != 2 || cfg.MinConns != 0 {
		t.Fatalf("unbounded H0012 pool: %+v %v", cfg, err)
	}
}

func TestH0012StoppedProducerDoesNotStopIngestion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopped, releaseIngestion := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runWorkerLoops(ctx, func(ctx context.Context) error {
			select {
			case <-releaseIngestion:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, func(context.Context) error { close(stopped); return bottlenecks.ErrSourceExpired })
	}()
	<-stopped
	select {
	case err := <-done:
		t.Fatalf("producer failure stopped ingestion: %v", err)
	default:
	}
	close(releaseIngestion)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestH0012CoordinatorJoinsCleanupBeforeReturning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, cleaning, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runWorkerLoops(ctx, func(ctx context.Context) error { <-ctx.Done(); return nil }, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(cleaning)
			<-release
			return nil
		})
	}()
	<-started
	cancel()
	<-cleaning
	select {
	case err := <-done:
		t.Fatalf("returned before producer cleanup: %v", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestH0012CoordinatorCancelsProducerOnIngestionFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	failure := errors.New("ingestion stopped")
	started, cleanup := make(chan struct{}), make(chan struct{})
	err := runWorkerLoops(ctx, func(context.Context) error { <-started; return failure }, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(cleanup)
		return nil
	})
	if !errors.Is(err, failure) {
		t.Fatal("ingestion failure suppressed")
	}
	select {
	case <-cleanup:
	default:
		t.Fatal("ingestion failed before producer joined")
	}
}

func TestH0012InvalidArgumentsFailWithoutCredentialsOrDatabase(t *testing.T) {
	t.Setenv("GCBA_TRANSPORT_CLIENT_ID", "")
	t.Setenv("GCBA_TRANSPORT_CLIENT_SECRET", "")
	t.Setenv("DATABASE_URL", "invalid-private-url")
	var output bytes.Buffer
	err := run(context.Background(), []string{"-h0012-model=experiment"}, slog.New(slog.DiscardHandler), &output)
	if err == nil || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "credentials") {
		t.Fatalf("invalid arguments did not fail first: %v", err)
	}
}
