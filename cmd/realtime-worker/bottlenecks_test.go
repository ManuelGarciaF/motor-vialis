package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
)

type fakeBottlenecksStore struct {
	ready                             bool
	model                             bottlenecks.PersistenceModel
	calls                             []string
	schemaErr, loadErr, activationErr error
}

func (s *fakeBottlenecksStore) ProducerSchemaReady(context.Context) (bool, error) {
	s.calls = append(s.calls, "schema")
	return s.ready, s.schemaErr
}
func (s *fakeBottlenecksStore) Activate(_ context.Context, model bottlenecks.PersistenceModel) (bottlenecks.ProcessingRun, error) {
	s.calls = append(s.calls, "activate")
	if s.activationErr == nil {
		s.model = model
		s.loadErr = nil
	}
	return bottlenecks.ProcessingRun{Model: s.model}, s.activationErr
}
func (s *fakeBottlenecksStore) LoadProducerStates(context.Context, string) (bottlenecks.ProcessingRun, []bottlenecks.ProducerState, error) {
	s.calls = append(s.calls, "load")
	return bottlenecks.ProcessingRun{Model: s.model}, nil, s.loadErr
}
func (s *fakeBottlenecksStore) ProcessNext(context.Context, string) (bottlenecks.ProducerProgress, error) {
	panic("initialization must not process a capture")
}

func TestBottlenecksPoolIsBoundedIndependently(t *testing.T) {
	cfg, err := bottlenecksPoolConfig("postgresql://localhost/vialis?pool_max_conns=20&pool_min_conns=10")
	if err != nil || cfg.MaxConns != config.BottlenecksPoolMaxConnections || cfg.MinConns != 0 || cfg.ConnConfig.RuntimeParams["application_name"] != "vialis-bottlenecks" {
		t.Fatalf("unbounded Bottlenecks pool: %+v %v", cfg, err)
	}
}

func TestBottlenecksStoppedProducerDoesNotStopIngestion(t *testing.T) {
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

func TestBottlenecksCoordinatorJoinsCleanupBeforeReturning(t *testing.T) {
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

func TestBottlenecksCoordinatorCancelsProducerOnIngestionFailure(t *testing.T) {
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

func TestBottlenecksInvalidArgumentsFailWithoutCredentialsOrDatabase(t *testing.T) {
	t.Setenv("GCBA_TRANSPORT_CLIENT_ID", "")
	t.Setenv("GCBA_TRANSPORT_CLIENT_SECRET", "")
	t.Setenv("DATABASE_URL", "invalid-private-url")
	var output bytes.Buffer
	err := run(context.Background(), []string{"-bottlenecks-activate"}, slog.New(slog.DiscardHandler), &output)
	if err == nil || strings.Contains(err.Error(), "private-") || strings.Contains(err.Error(), "credentials") {
		t.Fatalf("invalid arguments did not fail first: %v", err)
	}
}
