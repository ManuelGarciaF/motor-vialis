package precompute

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

type fakeRepository struct {
	targets     []Target
	saved       []int64
	saveErr     error
	afterID     int64
	limit       int
	onlyMissing bool
}

func (r *fakeRepository) Targets(_ context.Context, after int64, limit int, missing bool) ([]Target, error) {
	r.afterID, r.limit, r.onlyMissing = after, limit, missing
	return r.targets, nil
}
func (r *fakeRepository) Save(_ context.Context, t Target, _ Metrics) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved = append(r.saved, t.ID)
	return nil
}

type fakeLines func(context.Context, int64) (lines.Detail, error)

func (f fakeLines) Get(c context.Context, id int64) (lines.Detail, error) { return f(c, id) }

type fakeSimulator func(context.Context, simulation.Route) (simulation.Result, error)

func (f fakeSimulator) Simulate(c context.Context, r simulation.Route) (simulation.Result, error) {
	return f(c, r)
}
func options() Options {
	return Options{Jurisdiction: route.JurisdictionCABA, ModelVersion: "test", Timeout: time.Second}
}
func result() simulation.Result {
	return simulation.Result{Global: simulation.GlobalResult{Demand: simulation.DemandTotals{PotentialDemand: 123.6}, Revenue: simulation.RevenueTotals{PotentialRevenueCents: 456.5}}}
}
func goodLines(context.Context, int64) (lines.Detail, error) {
	return lines.Detail{Simulable: true}, nil
}
func goodSimulator(context.Context, simulation.Route) (simulation.Result, error) {
	return result(), nil
}

func TestRunContinuesPastInvalidAndFailedLines(t *testing.T) {
	repo := &fakeRepository{targets: []Target{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}}
	catalog := fakeLines(func(ctx context.Context, id int64) (lines.Detail, error) {
		switch id {
		case 1:
			return lines.Detail{}, &lines.NotSimulableError{LineID: id, Reason: "invalid"}
		case 2:
			return lines.Detail{Simulable: false, NotSimulableReason: "missing segment"}, nil
		case 3:
			return lines.Detail{}, errors.New("database read failed")
		}
		return lines.Detail{Simulable: true, Route: simulation.Route{Stops: []route.Stop{{ID: "ok"}}}}, nil
	})
	var events []Event
	sim := fakeSimulator(func(ctx context.Context, r simulation.Route) (simulation.Result, error) {
		if r.Jurisdiction != route.JurisdictionCABA {
			t.Fatal("missing jurisdiction")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		return result(), nil
	})
	o := options()
	o.AfterID = 10
	o.Limit = 20
	o.OnlyMissing = true
	summary, err := Run(context.Background(), repo, catalog, sim, o, func(e Event) error { events = append(events, e); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Selected: 5, Saved: 2, Skipped: 2, Failed: 1}) {
		t.Fatalf("summary=%+v", summary)
	}
	if !reflect.DeepEqual(repo.saved, []int64{4, 5}) {
		t.Fatal(repo.saved)
	}
	if repo.afterID != 10 || repo.limit != 20 || !repo.onlyMissing {
		t.Fatal("selection options not forwarded")
	}
	if *events[3].Metrics != (Metrics{Passengers: 124, Revenue: 457}) {
		t.Fatal(events[3])
	}
}

func TestRunDryRunNeverSaves(t *testing.T) {
	repo := &fakeRepository{targets: []Target{{ID: 1}}}
	o := options()
	o.DryRun = true
	summary, err := Run(context.Background(), repo, fakeLines(goodLines), fakeSimulator(goodSimulator), o, func(Event) error { return nil })
	if err != nil || summary.Calculated != 1 || len(repo.saved) != 0 {
		t.Fatalf("%+v %v", summary, err)
	}
}

func TestRunReportsSimulationAndSaveFailures(t *testing.T) {
	for _, stage := range []string{"simulation", "save"} {
		t.Run(stage, func(t *testing.T) {
			repo := &fakeRepository{targets: []Target{{ID: 1}, {ID: 2}}}
			sim := fakeSimulator(goodSimulator)
			if stage == "save" {
				repo.saveErr = errors.New("write failed")
			} else {
				sim = func(context.Context, simulation.Route) (simulation.Result, error) {
					return simulation.Result{}, errors.New("simulation failed")
				}
			}
			summary, err := Run(context.Background(), repo, fakeLines(goodLines), sim, options(), func(e Event) error {
				if e.Status != "failed" || e.Error == "" {
					t.Fatal(e)
				}
				return nil
			})
			if err != nil || summary.Failed != 2 || len(repo.saved) != 0 {
				t.Fatalf("%+v %v", summary, err)
			}
		})
	}
}

func TestRunStopsOnReportFailureAndCancellation(t *testing.T) {
	repo := &fakeRepository{targets: []Target{{ID: 1}, {ID: 2}}}
	sentinel := errors.New("disk full")
	_, err := Run(context.Background(), repo, fakeLines(goodLines), fakeSimulator(goodSimulator), options(), func(Event) error { return sentinel })
	if !errors.Is(err, sentinel) || len(repo.saved) != 1 {
		t.Fatalf("%v %v", err, repo.saved)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo.saved = nil
	_, err = Run(ctx, repo, fakeLines(goodLines), fakeSimulator(goodSimulator), options(), func(Event) error { return nil })
	if !errors.Is(err, context.Canceled) || len(repo.saved) != 0 {
		t.Fatal(err)
	}
}

func TestOptionsValidation(t *testing.T) {
	for _, mutate := range []func(*Options){
		func(o *Options) { o.Jurisdiction = "" }, func(o *Options) { o.Jurisdiction = "guess" },
		func(o *Options) { o.Timeout = 0 }, func(o *Options) { o.Limit = -1 }, func(o *Options) { o.AfterID = -1 }, func(o *Options) { o.ModelVersion = "" },
	} {
		o := options()
		mutate(&o)
		if o.Validate() == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
}

func TestRoundedMetricsRejectInvalidNumbers(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), -1, float64(math.MaxInt32) + 1} {
		r := result()
		r.Global.Demand.PotentialDemand = value
		if _, err := roundedMetrics(r); err == nil {
			t.Fatalf("accepted demand %v", value)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), -1, float64(math.MaxInt64)} {
		r := result()
		r.Global.Revenue.PotentialRevenueCents = value
		if _, err := roundedMetrics(r); err == nil {
			t.Fatalf("accepted revenue %v", value)
		}
	}
	r := simulation.Result{}
	if m, err := roundedMetrics(r); err != nil || m != (Metrics{}) {
		t.Fatalf("zero is a valid estimate: %v %v", m, err)
	}
}
