// Package precompute calculates catalog estimates using the regular simulator.
package precompute

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// Target includes the old catalog values so the report can serve as a backup.
type Target struct {
	ID         int64  `json:"id"`
	Passengers *int64 `json:"previousPassengers"`
	Revenue    *int64 `json:"previousRevenueCents"`
}

type Metrics struct {
	Passengers int64 `json:"passengers"`
	Revenue    int64 `json:"revenueCents"`
}

type Repository interface {
	Targets(context.Context, int64, int, bool) ([]Target, error)
	Save(context.Context, Target, Metrics) error
}

type Lines interface {
	Get(context.Context, int64) (lines.Detail, error)
}
type Simulator interface {
	Simulate(context.Context, simulation.Route) (simulation.Result, error)
}

type Options struct {
	Jurisdiction route.Jurisdiction
	ModelVersion string
	AfterID      int64
	Limit        int
	OnlyMissing  bool
	DryRun       bool
	Timeout      time.Duration
}

func (o Options) Validate() error {
	switch o.Jurisdiction {
	case route.JurisdictionCABA, route.JurisdictionProvince, route.JurisdictionNational:
	default:
		return fmt.Errorf("-jurisdiction must be caba, province or national (no default)")
	}
	if o.Timeout <= 0 || o.AfterID < 0 || o.Limit < 0 {
		return fmt.Errorf("timeout must be positive; after-id and limit must be nonnegative")
	}
	if o.ModelVersion == "" {
		return fmt.Errorf("model version is required")
	}
	return nil
}

type Event struct {
	Target
	Status  string             `json:"status"`
	Error   string             `json:"error,omitempty"`
	Metrics *Metrics           `json:"metrics,omitempty"`
	Result  *simulation.Result `json:"result,omitempty"`
}

type Summary struct {
	Selected   int `json:"selected"`
	Saved      int `json:"saved"`
	Calculated int `json:"calculated"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`
}

// Run is sequential to avoid saturating spatial queries. Successful rows commit
// independently; a failed line is reported and does not stop subsequent lines.
// Reporting failures and cancellation, unlike line errors, stop the whole run.
func Run(ctx context.Context, repo Repository, catalog Lines, simulator Simulator, options Options, report func(Event) error) (Summary, error) {
	var summary Summary
	if err := options.Validate(); err != nil {
		return summary, err
	}
	targets, err := repo.Targets(ctx, options.AfterID, options.Limit, options.OnlyMissing)
	if err != nil {
		return summary, err
	}
	summary.Selected = len(targets)
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		lineCtx, cancel := context.WithTimeout(ctx, options.Timeout)
		event := calculate(lineCtx, catalog, simulator, target, options)
		if event.Status == "calculated" && !options.DryRun {
			if err := repo.Save(lineCtx, target, *event.Metrics); err != nil {
				event.Status, event.Error = "failed", err.Error()
			} else {
				event.Status = "saved"
			}
		}
		cancel()
		switch event.Status {
		case "saved":
			summary.Saved++
		case "calculated":
			summary.Calculated++
		case "skipped":
			summary.Skipped++
		default:
			summary.Failed++
		}
		if err := report(event); err != nil {
			return summary, fmt.Errorf("write report: %w", err)
		}
	}
	return summary, ctx.Err()
}

func calculate(ctx context.Context, catalog Lines, simulator Simulator, target Target, options Options) Event {
	event := Event{Target: target, Status: "failed"}
	detail, err := catalog.Get(ctx, target.ID)
	if err != nil {
		var invalid *lines.NotSimulableError
		if errors.As(err, &invalid) {
			event.Status = "skipped"
		}
		event.Error = err.Error()
		return event
	}
	if !detail.Simulable {
		event.Status, event.Error = "skipped", detail.NotSimulableReason
		return event
	}
	input := detail.Route
	input.Jurisdiction = options.Jurisdiction
	result, err := simulator.Simulate(ctx, input)
	if err != nil {
		event.Error = err.Error()
		return event
	}
	metrics, err := roundedMetrics(result)
	if err != nil {
		event.Error = err.Error()
		return event
	}
	event.Status, event.Metrics, event.Result = "calculated", &metrics, &result
	return event
}

// Catalog counts use the same potential demand/revenue fields as MotorMapper.
// Do not overwrite the scheduled GTFS total time with modeled travel time.
func roundedMetrics(result simulation.Result) (Metrics, error) {
	passengers := math.Round(result.Global.Demand.PotentialDemand)
	revenue := math.Round(result.Global.Revenue.PotentialRevenueCents)
	if math.IsNaN(passengers) || math.IsInf(passengers, 0) || result.Global.Demand.PotentialDemand < 0 || passengers > math.MaxInt32 {
		return Metrics{}, fmt.Errorf("potential demand outside catalog INTEGER range")
	}
	// float64(math.MaxInt64) is 2^63, which is already outside BIGINT.
	if math.IsNaN(revenue) || math.IsInf(revenue, 0) || result.Global.Revenue.PotentialRevenueCents < 0 || revenue >= float64(math.MaxInt64) {
		return Metrics{}, fmt.Errorf("potential revenue outside catalog BIGINT range")
	}
	return Metrics{Passengers: int64(passengers), Revenue: int64(revenue)}, nil
}
