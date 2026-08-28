package main

import (
	"os"
	"strings"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/config"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

func TestDecodeRouteReadsGeoJSONPaths(t *testing.T) {
	input := `{
		"jurisdiction": "caba",
		"stops": [
			{
				"id": "A",
				"position": {"latitude": -34.6, "longitude": -58.38},
				"pathToNext": {
					"type": "LineString",
					"coordinates": [
						[-58.38, -34.6],
						[-58.39, -34.61]
					]
				}
			},
			{
				"id": "B",
				"position": {"latitude": -34.61, "longitude": -58.39}
			}
		]
	}`

	actual, err := decodeRoute(strings.NewReader(input), config.DefaultLinesPolicy().AlignmentToleranceMeters)
	if err != nil {
		t.Fatalf("decodeRoute() error = %v", err)
	}
	if len(actual.Stops) != 2 ||
		actual.Stops[0].PathToNext == nil ||
		len(actual.Stops[0].PathToNext.Positions) != 2 {
		t.Fatalf("route = %#v", actual)
	}
	if actual.Stops[0].PathToNext.Positions[0].Longitude != -58.38 {
		t.Fatalf("first path position = %#v", actual.Stops[0].PathToNext.Positions[0])
	}
}

func TestDecodeRouteAlignsStoredGTFSPathEndpoints(t *testing.T) {
	input := `{
		"jurisdiction": "caba",
		"stops": [
			{
				"id": "2031665",
				"position": {
					"latitude": -34.586005,
					"longitude": -58.373625
				},
				"pathToNext": {
					"type": "LineString",
					"coordinates": [
						[-58.373625, -34.586005],
						[-58.372723, -34.589460],
						[-58.372870385, -34.589648151]
					]
				}
			},
			{
				"id": "204232",
				"position": {
					"latitude": -34.589460,
					"longitude": -58.372723
				},
				"pathToNext": {
					"type": "LineString",
					"coordinates": [
						[-58.372870385, -34.589648151],
						[-58.374470, -34.591970],
						[-58.374740954, -34.592301286]
					]
				}
			},
			{
				"id": "204208",
				"position": {
					"latitude": -34.591970,
					"longitude": -58.374470
				}
			}
		]
	}`

	actual, err := decodeRoute(strings.NewReader(input), config.DefaultLinesPolicy().AlignmentToleranceMeters)
	if err != nil {
		t.Fatalf("decodeRoute() error = %v", err)
	}

	// The alignment rules themselves are covered in internal/lines; here it is
	// enough that decodeRoute applies them before returning.
	if err := route.Validate(actual); err != nil {
		t.Fatalf("aligned route validation error = %v", err)
	}
	firstPath := actual.Stops[0].PathToNext.Positions
	if firstPath[len(firstPath)-1] != actual.Stops[1].Position {
		t.Fatalf(
			"first path destination = %#v, want %#v",
			firstPath[len(firstPath)-1],
			actual.Stops[1].Position,
		)
	}
}

func TestDecodeRouteRejectsImplausibleEndpointAlignment(t *testing.T) {
	input := `{
		"jurisdiction": "caba",
		"stops": [
			{
				"id": "A",
				"position": {"latitude": -34.6, "longitude": -58.38},
				"pathToNext": {
					"type": "LineString",
					"coordinates": [
						[-58.40, -34.62],
						[-58.39, -34.61]
					]
				}
			},
			{
				"id": "B",
				"position": {"latitude": -34.61, "longitude": -58.39}
			}
		]
	}`

	if _, err := decodeRoute(strings.NewReader(input), config.DefaultLinesPolicy().AlignmentToleranceMeters); err == nil {
		t.Fatal("decodeRoute() error = nil")
	}
}

func TestDecodeRouteRejectsUnknownFields(t *testing.T) {
	_, err := decodeRoute(strings.NewReader(`{"stops":[],"unexpected":true}`), config.DefaultLinesPolicy().AlignmentToleranceMeters)
	if err == nil {
		t.Fatal("decodeRoute() error = nil")
	}
}

func TestDecodeRouteRejectsMultipleValues(t *testing.T) {
	_, err := decodeRoute(strings.NewReader(`{"stops":[]} {"stops":[]}`), config.DefaultLinesPolicy().AlignmentToleranceMeters)
	if err == nil {
		t.Fatal("decodeRoute() error = nil")
	}
}

func TestExampleRouteIsValid(t *testing.T) {
	inputFile, err := os.Open("../../examples/linea-132.json")
	if err != nil {
		t.Fatalf("open example route: %v", err)
	}
	defer inputFile.Close()

	input, err := decodeRoute(
		inputFile,
		config.DefaultLinesPolicy().AlignmentToleranceMeters,
	)
	if err != nil {
		t.Fatalf("decode example route: %v", err)
	}
	if err := route.Validate(input); err != nil {
		t.Fatalf("validate example route: %v", err)
	}
}
