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
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/demand"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/traveltime"
)

const maximumEndpointAlignmentMeters = 250.0

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
		traveltime.DefaultPolicy(),
	)
	service := simulation.NewService(demandEstimator, travelTimeEstimator)
	result, err := service.Simulate(ctx, input)
	if err != nil {
		log.Fatalf("simulate route: %v", err)
	}

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
	if err := alignStoredPathEndpoints(&input); err != nil {
		return simulation.Route{}, err
	}
	return input, nil
}

// alignStoredPathEndpoints adapts paths exported from stored GTFS routes.
//
// GTFS shape fractions can place a segment boundary close to, but not exactly
// on, its physical stop. The simulation's domain model remains strict; this
// test executable replaces only the first and last positions while preserving
// every intermediate point of the stored path.
func alignStoredPathEndpoints(input *simulation.Route) error {
	for index := 0; index < len(input.Stops)-1; index++ {
		path := input.Stops[index].PathToNext
		if path == nil || len(path.Positions) < 2 {
			continue
		}

		origin := input.Stops[index].Position
		destination := input.Stops[index+1].Position
		first := path.Positions[0]
		last := path.Positions[len(path.Positions)-1]
		startGap := route.DistanceMeters(first, origin)
		endGap := route.DistanceMeters(last, destination)
		forwardGap := startGap + endGap
		reverseGap := route.DistanceMeters(first, destination) +
			route.DistanceMeters(last, origin)

		// Do not hide a reversed LineString. The regular route validation will
		// report it with its domain-specific error.
		if reverseGap < forwardGap {
			continue
		}
		if startGap > maximumEndpointAlignmentMeters {
			return fmt.Errorf(
				"route.stops[%d].pathToNext first coordinate is %.1f meters "+
					"from the current stop; maximum automatic alignment is %.0f meters",
				index,
				startGap,
				maximumEndpointAlignmentMeters,
			)
		}
		if endGap > maximumEndpointAlignmentMeters {
			return fmt.Errorf(
				"route.stops[%d].pathToNext last coordinate is %.1f meters "+
					"from the next stop; maximum automatic alignment is %.0f meters",
				index,
				endGap,
				maximumEndpointAlignmentMeters,
			)
		}

		path.Positions[0] = origin
		path.Positions[len(path.Positions)-1] = destination
	}
	return nil
}
