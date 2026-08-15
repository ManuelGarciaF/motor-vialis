package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

func main() {
	cfg, err := config.FromEnv()
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}
	databaseURL := flag.String(
		"database-url",
		cfg.DatabaseURL,
		"PostgreSQL connection URL",
	)
	routeFile := flag.String(
		"route-file",
		"",
		"JSON file containing ordered stops and each pathToNext LineString",
	)
	flag.Parse()
	if *routeFile == "" {
		log.Fatal("-route-file is required")
	}

	inputFile, err := os.Open(*routeFile)
	if err != nil {
		log.Fatalf("open route file: %v", err)
	}
	defer inputFile.Close()
	input, err := decodeRoute(inputFile)
	if err != nil {
		log.Fatalf("decode route file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	database, err := postgres.Open(ctx, *databaseURL)
	if err != nil {
		log.Fatalf("connect to PostgreSQL: %v", err)
	}
	defer database.Close()

	demandEstimator := demand.NewService(
		postgres.NewDemandRepository(database),
		config.SimulationAccessRadiusMeters,
		cfg.SimulationAccessibilityCalculator,
	)
	travelTimeEstimator := traveltime.NewService(
		postgres.NewTravelTimeRepository(database),
		cfg.SimulationTravelTimePolicy,
	)
	revenueEstimator := revenue.NewService(
		postgres.NewRevenueRepository(database),
		revenue.Policy{
			CaptureFactor:       cfg.SimulationRevenueCaptureFactor,
			RegisteredCardShare: cfg.SimulationRegisteredCardShare,
		},
	)
	service := simulation.NewService(demandEstimator, travelTimeEstimator, revenueEstimator)
	result, err := service.Simulate(ctx, input)
	if err != nil {
		log.Fatalf("simulate route: %v", err)
	}
	result.Demand.ByStopPair = nil
	result.Metrics.TravelTime.BySegment = nil

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		log.Fatalf("encode result: %v", err)
	}
}

func decodeRoute(reader io.Reader) (simulation.Route, error) {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()

	var input simulation.Route
	if err := decoder.Decode(&input); err != nil {
		return simulation.Route{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return simulation.Route{}, fmt.Errorf("route file must contain one JSON value")
		}
		return simulation.Route{}, fmt.Errorf("decode trailing content: %w", err)
	}
	if err := lines.AlignStoredPathEndpoints(&input); err != nil {
		return simulation.Route{}, err
	}
	return input, nil
}
