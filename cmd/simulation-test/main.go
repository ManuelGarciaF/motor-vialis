package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/database/postgres"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation"
)

var defaultStops = line132Stops()

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
	stops := defaultStops.clone()
	flag.Var(
		&stops,
		"stop",
		`ordered stop in the form "ID,latitude,longitude"; the first value replaces the defaults`,
	)
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	database, err := postgres.Open(ctx, *databaseURL)
	if err != nil {
		log.Fatalf("connect to PostgreSQL: %v", err)
	}
	defer database.Close()

	repository := postgres.NewSimulationRepository(database)
	service := simulation.NewService(
		repository,
		config.SimulationAccessRadiusMeters,
		cfg.SimulationAccessibilityCalculator,
	)
	result, err := service.Simulate(ctx, simulation.Route{Stops: stops})
	if err != nil {
		log.Fatalf("simulate route: %v", err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		log.Fatalf("encode result: %v", err)
	}
}

type stopList []simulation.Stop

func (stops *stopList) Set(value string) error {
	parts := strings.Split(value, ",")
	if len(parts) != 3 {
		return fmt.Errorf("stop must have the form ID,latitude,longitude: %q", value)
	}

	stopID := strings.TrimSpace(parts[0])
	if stopID == "" {
		return fmt.Errorf("stop ID must not be empty")
	}
	latitude, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return fmt.Errorf("parse latitude for stop %q: %w", stopID, err)
	}
	longitude, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	if err != nil {
		return fmt.Errorf("parse longitude for stop %q: %w", stopID, err)
	}

	if reflectDefaults(*stops) {
		*stops = nil
	}
	*stops = append(*stops, simulation.Stop{
		ID: stopID,
		Position: simulation.Position{
			Latitude:  latitude,
			Longitude: longitude,
		},
	})
	return nil
}

func (stops *stopList) String() string {
	values := make([]string, len(*stops))
	for index, stop := range *stops {
		values[index] = fmt.Sprintf(
			"%s,%g,%g",
			stop.ID,
			stop.Position.Latitude,
			stop.Position.Longitude,
		)
	}
	return strings.Join(values, ";")
}

func (stops stopList) clone() stopList {
	return append(stopList(nil), stops...)
}

func reflectDefaults(stops stopList) bool {
	if len(stops) != len(defaultStops) {
		return false
	}
	for index := range stops {
		if stops[index] != defaultStops[index] {
			return false
		}
	}
	return true
}
