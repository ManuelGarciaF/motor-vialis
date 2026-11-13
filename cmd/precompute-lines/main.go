// precompute-lines fills catalog demand/revenue using the normal simulator.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/app"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/precompute"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg := config.FromEnv()
	flags := flag.NewFlagSet("precompute-lines", flag.ContinueOnError)
	flags.SetOutput(stderr)
	databaseURL := flags.String("database-url", cfg.DatabaseURL, "PostgreSQL connection URL (defaults to DATABASE_URL)")
	jurisdiction := flags.String("jurisdiction", "", "required: caba, province or national; applies to ALL selected lines")
	version := flags.String("model-version", "", "required provenance label; use the exact commit/build identifier")
	afterID := flags.Int64("after-id", 0, "process IDs strictly greater than this value")
	limit := flags.Int("limit", 0, "maximum selected lines; 0 selects all")
	onlyMissing := flags.Bool("only-missing", false, "select only lines missing demand or revenue; does not check version/jurisdiction freshness")
	dryRun := flags.Bool("dry-run", false, "calculate and report, without any database writes or schema changes")
	timeout := flags.Duration("timeout", 2*time.Minute, "timeout per line including load, simulation and save")
	reportPath := flags.String("report", "", "new JSONL report file (default .local/precompute-lines/<timestamp>.jsonl)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	options := precompute.Options{Jurisdiction: route.Jurisdiction(*jurisdiction), ModelVersion: *version, AfterID: *afterID, Limit: *limit, OnlyMissing: *onlyMissing, DryRun: *dryRun, Timeout: *timeout}
	if err := options.Validate(); err != nil {
		return err
	}

	// Create the report before opening the DB or making any changes. Never clobber
	// a previous report, and sync each record so partial runs remain inspectable.
	if *reportPath == "" {
		*reportPath = filepath.Join(".local", "precompute-lines", time.Now().UTC().Format("20060102T150405.000000000Z")+".jsonl")
	}
	if err := os.MkdirAll(filepath.Dir(*reportPath), 0700); err != nil {
		return err
	}
	report, err := os.OpenFile(*reportPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create report: %w", err)
	}
	defer report.Close()
	encoder := json.NewEncoder(report)
	write := func(value any) error {
		if err := encoder.Encode(value); err != nil {
			return err
		}
		return report.Sync()
	}
	if err := write(struct {
		Type         string             `json:"type"`
		StartedAt    time.Time          `json:"startedAt"`
		Jurisdiction string             `json:"jurisdiction"`
		ModelVersion string             `json:"modelVersion"`
		Options      precompute.Options `json:"options"`
	}{"start", time.Now().UTC(), *jurisdiction, *version, options}); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "All selected lines use jurisdiction=%s. Report: %s\n", *jurisdiction, *reportPath)

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	database, err := postgres.Open(connectCtx, *databaseURL)
	cancel()
	if err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	defer database.Close()
	repo := postgres.NewPrecomputeRepository(database)
	summary, runErr := precompute.Run(ctx, repo, app.NewLinesService(database), app.NewSimulationService(database), options, func(event precompute.Event) error {
		if err := write(event); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "id=%d status=%s %s\n", event.ID, event.Status, event.Error)
		return nil
	})
	reportErr := write(struct {
		Type    string             `json:"type"`
		Summary precompute.Summary `json:"summary"`
		Error   string             `json:"error,omitempty"`
	}{"finish", summary, errorText(runErr)})
	outputErr := json.NewEncoder(stdout).Encode(summary)
	if err := errors.Join(runErr, reportErr, outputErr); err != nil {
		return err
	}
	if summary.Failed > 0 {
		return fmt.Errorf("%d lines failed; inspect %s (successful lines remain saved)", summary.Failed, *reportPath)
	}
	return nil
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
