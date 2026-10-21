package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/ManuelGarciaF/vialis-motor/internal/config"
)

const (
	h0012MeasurementVersion = "commercial-v1"
	h0012MatcherVersion     = "conservative-v1"
	// These limits bound one scheduling turn and DB resources, not a sequence.
	h0012PoolMaxConnections  = 2
	h0012CycleTimeout        = 5 * time.Minute
	h0012MaxCapturesPerCycle = 32
)

type workerOptions struct {
	interval time.Duration
	h0012    h0012Options
}

type h0012Options struct {
	model       string
	activate    bool
	interval    time.Duration
	segment     float64
	maximumGap  time.Duration
	maxDistance float64
	maxSpeed    float64
}

func parseWorkerOptions(args []string, output io.Writer) (workerOptions, error) {
	var options workerOptions
	flags := flag.NewFlagSet("realtime-worker", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.DurationVar(&options.interval, "interval", config.RealtimePollInterval, "wait after each completed capture cycle (e.g. 1m)")
	flags.StringVar(&options.h0012.model, "h0012-model", "", "explicitly activated H0012 model to resume")
	flags.BoolVar(&options.h0012.activate, "h0012-activate", false, "explicitly initialize the H0012 model without backfill")
	flags.DurationVar(&options.h0012.interval, "h0012-interval", 0, "required explicit wait between H0012 processing cycles")
	flags.Float64Var(&options.h0012.segment, "h0012-segment-meters", 0, "required on activation: experimental nominal sector length")
	flags.DurationVar(&options.h0012.maximumGap, "h0012-max-gap", 0, "required on activation: maximum supporting GPS gap")
	flags.Float64Var(&options.h0012.maxDistance, "h0012-max-distance-meters", 0, "required on activation: maximum shape distance")
	flags.Float64Var(&options.h0012.maxSpeed, "h0012-max-speed-kph", 0, "required on activation: maximum longitudinal speed")
	if err := flags.Parse(args); err != nil {
		return workerOptions{}, err
	}
	if flags.NArg() != 0 || options.interval <= 0 {
		return workerOptions{}, fmt.Errorf("invalid realtime-worker arguments")
	}

	provided := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if err := options.h0012.validate(provided); err != nil {
		return workerOptions{}, err
	}
	return options, nil
}

func (options h0012Options) validate(provided map[string]bool) error {
	if options.model == "" {
		for name := range provided {
			if strings.HasPrefix(name, "h0012-") {
				return fmt.Errorf("H0012 options require -h0012-model")
			}
		}
		return nil
	}
	if strings.TrimSpace(options.model) == "" || !provided["h0012-interval"] || options.interval <= 0 {
		return fmt.Errorf("H0012 requires a nonempty model and explicit positive -h0012-interval")
	}
	measurementFlags := []string{"h0012-segment-meters", "h0012-max-gap", "h0012-max-distance-meters", "h0012-max-speed-kph"}
	for _, name := range measurementFlags {
		if provided[name] != options.activate {
			return fmt.Errorf("H0012 measurement parameters must all be explicit on activation and omitted on resume")
		}
	}
	if options.activate {
		if err := options.persistenceModel().Validate(); err != nil {
			return fmt.Errorf("invalid explicit H0012 model parameters")
		}
	}
	return nil
}

func (options h0012Options) persistenceModel() bottlenecks.PersistenceModel {
	return bottlenecks.PersistenceModel{
		Name: options.model, MeasurementVersion: h0012MeasurementVersion, MatcherVersion: h0012MatcherVersion,
		NominalMeters: options.segment,
		Policy:        bottlenecks.MeasurementPolicy{MaximumGap: options.maximumGap, MaximumDistanceMeters: options.maxDistance, MaximumSpeedKPH: options.maxSpeed},
		TimeZone:      config.BottlenecksAnalysisTimeZone, RawRetentionDays: config.RealtimeRawRetentionDays,
		ClosureCaptures: bottlenecks.MissingCaptureLimit,
	}
}
