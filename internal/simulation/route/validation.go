package route

import (
	"fmt"
	"math"
	"strings"
)

const (
	endpointToleranceMeters = 20.0
	earthRadiusMeters       = 6371008.8
)

// ValidationError reports an invalid route input.
type ValidationError struct {
	Field   string
	Message string
}

func (err *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Message)
}

// Rooted returns a copy of the error whose field path starts at root instead
// of the "route" prefix Validate uses.
//
// A caller validating more than one route in the same request needs the error
// to say which of them failed, and "route.stops[3].id" cannot.
func (err *ValidationError) Rooted(root string) *ValidationError {
	field, found := strings.CutPrefix(err.Field, "route")
	if found {
		field = root + field
	} else {
		field = root + "." + err.Field
	}
	return &ValidationError{Field: field, Message: err.Message}
}

// Validate checks the complete route contract used by every estimator.
func Validate(input Route) error {
	if input.Jurisdiction != JurisdictionCABA &&
		input.Jurisdiction != JurisdictionProvince &&
		input.Jurisdiction != JurisdictionNational {
		return &ValidationError{
			Field:   "route.jurisdiction",
			Message: "must be caba, province, or national",
		}
	}
	if len(input.Stops) < 2 {
		return &ValidationError{
			Field:   "route.stops",
			Message: "must contain at least two stops",
		}
	}

	stopIDs := make(map[string]struct{}, len(input.Stops))
	for index, stop := range input.Stops {
		field := fmt.Sprintf("route.stops[%d]", index)
		stopID := strings.TrimSpace(stop.ID)
		if stopID == "" {
			return &ValidationError{
				Field:   field + ".id",
				Message: "must not be empty",
			}
		}
		if _, exists := stopIDs[stopID]; exists {
			return &ValidationError{
				Field:   field + ".id",
				Message: "must be unique within the route",
			}
		}
		stopIDs[stopID] = struct{}{}
		if err := validatePosition(field+".position", stop.Position); err != nil {
			return err
		}
	}

	for index := range input.Stops {
		stop := input.Stops[index]
		field := fmt.Sprintf("route.stops[%d].pathToNext", index)
		if index == len(input.Stops)-1 {
			if stop.PathToNext != nil {
				return &ValidationError{
					Field:   field,
					Message: "must be omitted for the last stop",
				}
			}
			continue
		}
		if stop.PathToNext == nil {
			return &ValidationError{
				Field:   field,
				Message: "is required except for the last stop",
			}
		}
		if err := validatePath(
			field,
			*stop.PathToNext,
			stop.Position,
			input.Stops[index+1].Position,
		); err != nil {
			return err
		}
	}
	return nil
}

func validatePath(
	field string,
	path LineString,
	origin Position,
	destination Position,
) error {
	if len(path.Positions) < 2 {
		return &ValidationError{
			Field:   field + ".coordinates",
			Message: "must contain at least two positions",
		}
	}
	for index, position := range path.Positions {
		if err := validatePosition(
			fmt.Sprintf("%s.coordinates[%d]", field, index),
			position,
		); err != nil {
			return err
		}
	}

	first := path.Positions[0]
	last := path.Positions[len(path.Positions)-1]
	firstToOrigin := DistanceMeters(first, origin)
	lastToDestination := DistanceMeters(last, destination)
	firstToDestination := DistanceMeters(first, destination)
	lastToOrigin := DistanceMeters(last, origin)
	if firstToDestination <= endpointToleranceMeters &&
		lastToOrigin <= endpointToleranceMeters &&
		firstToDestination+lastToOrigin <
			firstToOrigin+lastToDestination {
		return &ValidationError{
			Field:   field,
			Message: "must be ordered from the current stop to the next stop",
		}
	}
	if firstToOrigin > endpointToleranceMeters {
		return &ValidationError{
			Field:   field + ".coordinates[0]",
			Message: "must be within 20 meters of the current stop",
		}
	}
	if lastToDestination > endpointToleranceMeters {
		return &ValidationError{
			Field: fmt.Sprintf(
				"%s.coordinates[%d]",
				field,
				len(path.Positions)-1,
			),
			Message: "must be within 20 meters of the next stop",
		}
	}

	var lengthMeters float64
	for index := 0; index < len(path.Positions)-1; index++ {
		lengthMeters += DistanceMeters(
			path.Positions[index],
			path.Positions[index+1],
		)
	}
	if lengthMeters <= 0 {
		return &ValidationError{
			Field:   field,
			Message: "must have a positive length",
		}
	}
	return nil
}

func validatePosition(field string, position Position) error {
	if !finite(position.Latitude) ||
		position.Latitude < -90 ||
		position.Latitude > 90 {
		return &ValidationError{
			Field:   field + ".latitude",
			Message: "must be between -90 and 90",
		}
	}
	if !finite(position.Longitude) ||
		position.Longitude < -180 ||
		position.Longitude > 180 {
		return &ValidationError{
			Field:   field + ".longitude",
			Message: "must be between -180 and 180",
		}
	}
	return nil
}

// DistanceMeters calculates the great-circle distance between two positions.
func DistanceMeters(left, right Position) float64 {
	leftLatitude := radians(left.Latitude)
	rightLatitude := radians(right.Latitude)
	latitudeDelta := rightLatitude - leftLatitude
	longitudeDelta := radians(right.Longitude - left.Longitude)

	sineLatitude := math.Sin(latitudeDelta / 2)
	sineLongitude := math.Sin(longitudeDelta / 2)
	haversine := sineLatitude*sineLatitude +
		math.Cos(leftLatitude)*math.Cos(rightLatitude)*
			sineLongitude*sineLongitude
	haversine = math.Min(1, math.Max(0, haversine))
	return 2 * earthRadiusMeters * math.Asin(math.Sqrt(haversine))
}

func radians(degrees float64) float64 {
	return degrees * math.Pi / 180
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
