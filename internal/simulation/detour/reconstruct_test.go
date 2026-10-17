package detour

import (
	"math"
	"testing"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// Una calle que corre al Este, con la parada del medio colgada como apendice: la
// geometria que llega y la que sale pasan por el MISMO vertice sobre la calle
// (a ~9 m de la parada) antes de pinchar hacia ella. Antes del fix, el vecino de
// los dos lados caia sobre ese vertice compartido, before == after y el rumbo
// daba 0; con rumbo 0 la parada enganchaba a una calle lateral en vez de la
// avenida (el recuadro al pedo sobre el Metrobus).
func TestStopDirectionIgnoresSharedVertexOnAppendageStop(t *testing.T) {
	const (
		baseLatitude  = -34.5
		baseLongitude = -58.5
		vertexEast    = -58.4995  // ~46 m al Este del inicio
		nextEast      = -58.4990  // ~46 m mas al Este
		pokeNorth     = -34.49992 // ~9 m al Norte de la calle
	)
	input := route.Route{
		Stops: []route.Stop{
			{
				ID:       "a",
				Position: route.Position{Latitude: baseLatitude, Longitude: baseLongitude},
				PathToNext: &route.LineString{Positions: []route.Position{
					{Latitude: baseLatitude, Longitude: baseLongitude},
					{Latitude: baseLatitude, Longitude: vertexEast},
					{Latitude: pokeNorth, Longitude: vertexEast},
				}},
			},
			{
				ID:       "stop",
				Position: route.Position{Latitude: pokeNorth, Longitude: vertexEast},
				PathToNext: &route.LineString{Positions: []route.Position{
					{Latitude: pokeNorth, Longitude: vertexEast},
					{Latitude: baseLatitude, Longitude: vertexEast},
					{Latitude: baseLatitude, Longitude: nextEast},
				}},
			},
			{
				ID:       "b",
				Position: route.Position{Latitude: baseLatitude, Longitude: nextEast},
			},
		},
	}

	direction := stopDirection(input, 1)

	// La calle corre al Este: el rumbo correcto es ~90 grados, no el 0 espurio.
	if math.Abs(direction-90) > 5 {
		t.Fatalf("stop direction = %.1f, want the street bearing (~90)", direction)
	}
}

// Una parada normal (sin apendice) conserva el rumbo pasante before -> after.
func TestStopDirectionUsesThroughBearing(t *testing.T) {
	input := route.Route{
		Stops: []route.Stop{
			{
				ID:       "a",
				Position: route.Position{Latitude: -34.5, Longitude: -58.5},
				PathToNext: &route.LineString{Positions: []route.Position{
					{Latitude: -34.5, Longitude: -58.5},
					{Latitude: -34.5, Longitude: -58.4995},
				}},
			},
			{
				ID:       "stop",
				Position: route.Position{Latitude: -34.5, Longitude: -58.4995},
				PathToNext: &route.LineString{Positions: []route.Position{
					{Latitude: -34.5, Longitude: -58.4995},
					{Latitude: -34.5, Longitude: -58.499},
				}},
			},
			{
				ID:       "b",
				Position: route.Position{Latitude: -34.5, Longitude: -58.499},
			},
		},
	}

	direction := stopDirection(input, 1)

	if math.Abs(direction-90) > 5 {
		t.Fatalf("stop direction = %.1f, want ~90", direction)
	}
}
