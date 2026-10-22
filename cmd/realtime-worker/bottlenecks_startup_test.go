package main

import (
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

func TestWorkerNeedsNoBottlenecksFlags(t *testing.T) {
	options, err := parseWorkerOptions(nil, io.Discard)
	if err != nil || options.interval != config.RealtimePollInterval {
		t.Fatalf("default invocation: %+v %v", options, err)
	}
	for _, arg := range []string{
		"-bottlenecks-model=experiment", "-bottlenecks-activate",
		"-bottlenecks-interval=2m", "-bottlenecks-segment-meters=500",
		"-bottlenecks-max-gap=180s", "-bottlenecks-max-distance-meters=100", "-bottlenecks-max-speed-kph=100",
	} {
		if _, err := parseWorkerOptions([]string{arg}, io.Discard); err == nil {
			t.Fatalf("removed flag accepted: %s", arg)
		}
	}
}

func TestBottlenecksAutomaticallyInitializesOnlyMissingConfiguration(t *testing.T) {
	store := &fakeBottlenecksStore{ready: true, loadErr: bottlenecks.ErrModelNotFound}
	if err := initializeBottlenecks(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.calls, []string{"schema", "load", "activate", "load"}) || store.model != config.BottlenecksModel() {
		t.Fatalf("initialization did not use config: %+v", store)
	}
	store.calls = nil
	if err := initializeBottlenecks(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.calls, []string{"schema", "load"}) {
		t.Fatalf("restart reactivated or reset configuration: %v", store.calls)
	}
}

func TestBottlenecksStartupRejectsErrorsWithoutResettingConfiguration(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*fakeBottlenecksStore)
	}{
		{"schema", func(s *fakeBottlenecksStore) { s.ready = false }},
		{"schema error", func(s *fakeBottlenecksStore) { s.schemaErr = errors.New("private-schema-detail") }},
		{"driver error", func(s *fakeBottlenecksStore) { s.loadErr = errors.New("private-database-url") }},
		{"name", func(s *fakeBottlenecksStore) { s.model.Name = "other" }},
		{"segment", func(s *fakeBottlenecksStore) { s.model.NominalMeters++ }},
		{"gap", func(s *fakeBottlenecksStore) { s.model.Policy.MaximumGap += time.Second }},
		{"radius", func(s *fakeBottlenecksStore) { s.model.Policy.MaximumDistanceMeters++ }},
		{"speed", func(s *fakeBottlenecksStore) { s.model.Policy.MaximumSpeedKPH++ }},
		{"zone", func(s *fakeBottlenecksStore) { s.model.TimeZone = "UTC" }},
		{"measurement version", func(s *fakeBottlenecksStore) { s.model.MeasurementVersion = "other" }},
		{"matcher version", func(s *fakeBottlenecksStore) { s.model.MatcherVersion = "other" }},
		{"retention", func(s *fakeBottlenecksStore) { s.model.RawRetentionDays = 3 }},
		{"closure", func(s *fakeBottlenecksStore) { s.model.ClosureCaptures = 3 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeBottlenecksStore{ready: true, model: config.BottlenecksModel()}
			test.edit(store)
			err := initializeBottlenecks(context.Background(), store)
			var startup *startupError
			if !errors.As(err, &startup) || strings.Contains(err.Error(), "private-") {
				t.Fatalf("missing/unsafe error: %v", err)
			}
			for _, call := range store.calls {
				if call == "activate" {
					t.Fatal("error or policy mismatch reset existing configuration")
				}
			}
		})
	}
	store := &fakeBottlenecksStore{ready: true, loadErr: bottlenecks.ErrModelNotFound, activationErr: bottlenecks.ErrModelChanged}
	if err := initializeBottlenecks(context.Background(), store); err == nil || !reflect.DeepEqual(store.calls, []string{"schema", "load", "activate"}) {
		t.Fatalf("failed initialization was ignored: %v %v", store.calls, err)
	}
}

func TestBottlenecksIsAlwaysPrepared(t *testing.T) {
	run, _, err := prepareBottlenecks(context.Background(), "invalid-private-url", slog.New(slog.DiscardHandler))
	if err == nil || run != nil || strings.Contains(err.Error(), "invalid-private-url") {
		t.Fatalf("producer was skipped or exposed connection details: %v", err)
	}
}
