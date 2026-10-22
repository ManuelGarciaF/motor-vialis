// bottlenecks-analysis manually inspects historical GPS in a read-only snapshot.
// It does not ingest, persist operational passages, or classify bottlenecks.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Execution budgets are technical limits, not GPS validity/model thresholds.
func analysisBudget(all bool) time.Duration {
	if all {
		return 15 * time.Minute
	}
	return 5 * time.Minute
}

func run() error {
	id := flag.Int64("line-id", 0, "Stored GTFS recorrido ID; required unless -all")
	all := flag.Bool("all", false, "Analyze all targets observed through active H0011 equivalences; emit NDJSON with completion marker")
	from := flag.String("from", "", "Inclusive RFC3339 GPS timestamp (required)")
	to := flag.String("to", "", "Exclusive RFC3339 GPS timestamp (required)")
	nominal := flag.Float64("segment-meters", config.BottlenecksSegmentMeters, "Experimental nominal sector length")
	gap := flag.Duration("max-gap", config.BottlenecksMaximumGap, "Experimental maximum GPS gap")
	distance := flag.Float64("max-distance", config.BottlenecksMaximumDistanceMeters, "Experimental maximum geodesic matching distance (m)")
	speed := flag.Float64("max-speed", config.BottlenecksMaximumSpeedKPH, "Experimental maximum longitudinal speed (km/h)")
	url := flag.String("database-url", "", "Optional connection override; otherwise DATABASE_URL")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}

	start, err := time.Parse(time.RFC3339Nano, *from)
	if err != nil {
		return fmt.Errorf("-from must be an RFC3339 timestamp")
	}

	end, err := time.Parse(time.RFC3339Nano, *to)
	if err != nil {
		return fmt.Errorf("-to must be an RFC3339 timestamp")
	}
	if *all && *id != 0 {
		return fmt.Errorf("-all and -line-id are mutually exclusive")
	}

	validationID := *id
	if *all {
		validationID = 1
	} // Batch request validation; actual IDs come from the manifest.
	request := bottlenecks.AnalysisRequest{LineID: validationID, TimeZone: config.BottlenecksAnalysisTimeZone, Window: bottlenecks.Window{From: start.UTC(), To: end.UTC()},
		NominalMeters: *nominal, Policy: bottlenecks.MeasurementPolicy{MaximumGap: *gap, MaximumDistanceMeters: *distance, MaximumSpeedKPH: *speed}}
	if err := request.Validate(); err != nil {
		return err
	}
	if *url == "" {
		*url = config.FromEnv().DatabaseURL
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithTimeout(parent, analysisBudget(*all))
	defer cancel()

	db, err := postgres.Open(ctx, *url)
	if err != nil {
		return fmt.Errorf("cannot connect to PostgreSQL; check DATABASE_URL and connectivity")
	}
	defer db.Close()

	repo := postgres.NewBottlenecksRepository(db)
	if *all {
		targets, err := repo.ListTargets(ctx, request.Window)
		if err != nil {
			return err
		}

		encoder := json.NewEncoder(os.Stdout)
		header := struct {
			Type                   string
			Window                 bottlenecks.Window
			NominalMeters          float64
			Policy                 bottlenecks.AnalysisLimits
			TimeZone               string
			Targets                []bottlenecks.AnalysisTarget
			ExecutionBudgetSeconds float64
		}{Type: "header", Window: request.Window, NominalMeters: request.NominalMeters,
			Policy:   bottlenecks.AnalysisLimits{MaximumGapSeconds: request.Policy.MaximumGap.Seconds(), MaximumDistanceMeters: request.Policy.MaximumDistanceMeters, MaximumSpeedKPH: request.Policy.MaximumSpeedKPH},
			TimeZone: request.TimeZone, Targets: targets, ExecutionBudgetSeconds: analysisBudget(*all).Seconds()}
		if err := encoder.Encode(header); err != nil {
			return err
		}

		processed := 0
		summary, err := bottlenecks.AnalyzeTargets(ctx, repo, targets, request, func(report bottlenecks.Analysis) error {
			if err := encoder.Encode(struct {
				Type     string
				Analysis bottlenecks.Analysis
			}{Type: "analysis", Analysis: report}); err != nil {
				return err
			}

			processed++
			fmt.Fprintf(os.Stderr, "[%d/%d] target %d: %d complete passages\n", processed, len(targets), report.LineID, report.Coverage.ValidPassages)
			return nil
		})
		if err != nil {
			return err
		} // No completion marker: partial output must not be consolidated.
		return encoder.Encode(struct {
			Type    string
			Summary bottlenecks.BatchSummary
		}{Type: "complete", Summary: summary})
	}

	result, err := bottlenecks.Analyze(ctx, repo, request)
	if err != nil {
		return fmt.Errorf("analyze passages: %w", err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
