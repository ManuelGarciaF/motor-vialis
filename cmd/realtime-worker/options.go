package main

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
)

type workerOptions struct {
	interval time.Duration
}

func parseWorkerOptions(args []string, output io.Writer) (workerOptions, error) {
	var options workerOptions
	flags := flag.NewFlagSet("realtime-worker", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.DurationVar(&options.interval, "interval", config.RealtimePollInterval, "wait after each completed capture cycle (e.g. 1m)")
	if err := flags.Parse(args); err != nil {
		return workerOptions{}, err
	}
	if flags.NArg() != 0 || options.interval <= 0 {
		return workerOptions{}, fmt.Errorf("invalid realtime-worker arguments")
	}
	return options, nil
}
