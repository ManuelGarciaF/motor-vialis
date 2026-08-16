// Package lines exports the stored AMBA lines as simulation routes.
package lines

import (
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// AlignmentToleranceMeters is the largest gap this package closes between the
// endpoint of a stored path and the stop it belongs to.
//
// It is far wider than the tolerance route.Validate accepts because the two
// answer different questions: route.Validate decides whether a caller sent
// coherent geometry, while this constant decides whether a stored path is
// recognisably the same place as its stop.
const AlignmentToleranceMeters = 250.0

// GapError reports a stored path whose endpoint is too far from its stop to be
// treated as the same place.
type GapError struct {
	StopIndex int
	AtStart   bool
	GapMeters float64
}

func (err *GapError) Error() string {
	endpoint := "last"
	stop := "next stop"
	if err.AtStart {
		endpoint = "first"
		stop = "current stop"
	}
	return fmt.Sprintf(
		"stops[%d].pathToNext %s coordinate is %.1f meters from the %s; "+
			"maximum automatic alignment is %.0f meters",
		err.StopIndex,
		endpoint,
		err.GapMeters,
		stop,
		AlignmentToleranceMeters,
	)
}

// AlignStoredPathEndpoints adapts paths exported from stored GTFS routes.
//
// A GTFS shape fraction can place a segment boundary close to, but not exactly
// on, its physical stop. Replacing only the first and last position, and never
// an intermediate one, turns such a path into geometry route.Validate accepts
// without altering the itinerary it describes.
//
// This runs when the engine exports its own stored data, never on geometry a
// caller sent: input keeps being validated against the strict tolerance.
func AlignStoredPathEndpoints(input *route.Route) error {
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
		if startGap > AlignmentToleranceMeters {
			return &GapError{StopIndex: index, AtStart: true, GapMeters: startGap}
		}
		if endGap > AlignmentToleranceMeters {
			return &GapError{StopIndex: index, GapMeters: endGap}
		}

		path.Positions[0] = origin
		path.Positions[len(path.Positions)-1] = destination
	}
	return nil
}
