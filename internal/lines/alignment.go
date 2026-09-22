// Package lines exports the stored AMBA lines as simulation routes.
package lines

import (
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// GapError reports a stored path whose endpoint is too far from its stop to be
// treated as the same place.
type GapError struct {
	StopIndex       int
	AtStart         bool
	GapMeters       float64
	ToleranceMeters float64
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
		err.ToleranceMeters,
	)
}

// AlignStoredPathEndpoints snaps nearby GTFS segment endpoints to their stops.
// It only adjusts stored geometry; caller input remains subject to strict validation.
func AlignStoredPathEndpoints(input *route.Route, toleranceMeters float64) error {
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

		// Leave reversed paths untouched so route validation can reject them.
		if reverseGap < forwardGap {
			continue
		}
		if startGap > toleranceMeters {
			return &GapError{
				StopIndex:       index,
				AtStart:         true,
				GapMeters:       startGap,
				ToleranceMeters: toleranceMeters,
			}
		}
		if endGap > toleranceMeters {
			return &GapError{
				StopIndex:       index,
				GapMeters:       endGap,
				ToleranceMeters: toleranceMeters,
			}
		}

		path.Positions[0] = origin
		path.Positions[len(path.Positions)-1] = destination
	}
	return nil
}
