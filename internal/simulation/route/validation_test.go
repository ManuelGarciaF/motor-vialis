package route

import (
	"errors"
	"math"
	"testing"
)

func TestValidateAcceptsCompleteRoute(t *testing.T) {
	if err := Validate(validRoute()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsInvalidRoute(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Route)
		field  string
	}{
		{
			name: "not enough stops",
			mutate: func(input *Route) {
				input.Stops = input.Stops[:1]
				input.Stops[0].PathToNext = nil
			},
			field: "route.stops",
		},
		{
			name: "empty ID",
			mutate: func(input *Route) {
				input.Stops[1].ID = " "
			},
			field: "route.stops[1].id",
		},
		{
			name: "duplicate ID",
			mutate: func(input *Route) {
				input.Stops[1].ID = "A"
			},
			field: "route.stops[1].id",
		},
		{
			name: "invalid latitude",
			mutate: func(input *Route) {
				input.Stops[0].Position.Latitude = math.Inf(1)
			},
			field: "route.stops[0].position.latitude",
		},
		{
			name: "missing path",
			mutate: func(input *Route) {
				input.Stops[0].PathToNext = nil
			},
			field: "route.stops[0].pathToNext",
		},
		{
			name: "path on last stop",
			mutate: func(input *Route) {
				input.Stops[1].PathToNext = &LineString{
					Positions: []Position{
						input.Stops[1].Position,
						input.Stops[1].Position,
					},
				}
			},
			field: "route.stops[1].pathToNext",
		},
		{
			name: "not enough path positions",
			mutate: func(input *Route) {
				input.Stops[0].PathToNext.Positions = input.Stops[0].PathToNext.Positions[:1]
			},
			field: "route.stops[0].pathToNext.coordinates",
		},
		{
			name: "path starts too far",
			mutate: func(input *Route) {
				input.Stops[0].PathToNext.Positions[0].Latitude -= 0.01
			},
			field: "route.stops[0].pathToNext.coordinates[0]",
		},
		{
			name: "path ends too far",
			mutate: func(input *Route) {
				input.Stops[0].PathToNext.Positions[1].Latitude -= 0.01
			},
			field: "route.stops[0].pathToNext.coordinates[1]",
		},
		{
			name: "reversed path",
			mutate: func(input *Route) {
				positions := input.Stops[0].PathToNext.Positions
				positions[0], positions[1] = positions[1], positions[0]
			},
			field: "route.stops[0].pathToNext",
		},
		{
			name: "zero length path",
			mutate: func(input *Route) {
				input.Stops[1].Position = input.Stops[0].Position
				input.Stops[0].PathToNext.Positions[1] = input.Stops[0].Position
			},
			field: "route.stops[0].pathToNext",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validRoute()
			test.mutate(&input)
			err := Validate(input)
			if err == nil {
				t.Fatal("Validate() error = nil")
			}
			var validationError *ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("error type = %T, want *ValidationError", err)
			}
			if validationError.Field != test.field {
				t.Fatalf(
					"field = %q, want %q; error = %v",
					validationError.Field,
					test.field,
					err,
				)
			}
		})
	}
}

func validRoute() Route {
	origin := Position{Latitude: -34.6000, Longitude: -58.3800}
	destination := Position{Latitude: -34.6010, Longitude: -58.3810}
	return Route{Stops: []Stop{
		{
			ID:       "A",
			Position: origin,
			PathToNext: &LineString{Positions: []Position{
				origin,
				destination,
			}},
		},
		{ID: "B", Position: destination},
	}}
}
