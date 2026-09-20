package lines

import (
	"math"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// DrawnPath folds a route into the single continuous path its corridor is
// measured against.
//
// A route describes its geometry one segment at a time, but corridor coverage
// is a ratio over the whole drawn line: measured segment by segment, a route
// whose first half follows an existing line and whose second half leaves it
// would report two unrelated numbers instead of the one answer — "half of what
// you drew is already served" — that the caller asked for.
//
// A stop without a pathToNext is joined to the next one by the straight
// segment between them. That is a guess, and it is only acceptable here: a
// straight line between two stops is a poor description of an itinerary, which
// is why route.Validate refuses it before a simulation, but it is a fair
// approximation of which corridor the drawn route passes through, and refusing
// it would mean rejecting a sketch someone is still drawing.
//
// This lives in Go rather than in SQL because it is a rule about the route
// model, testable without a database, and because the database should receive
// one geometry instead of reassembling the caller's stops on every query.
//
// It assumes a route that passed validateDrawnRoute: with fewer than two stops
// there is no segment to walk and the result is an empty path.
func DrawnPath(input route.Route) route.LineString {
	positions := make([]route.Position, 0, len(input.Stops))
	// Consecutive segments share their joint, and a repeated coordinate adds
	// nothing to a LineString while giving PostGIS a zero-length piece to
	// measure.
	appendPosition := func(position route.Position) {
		if len(positions) > 0 && positions[len(positions)-1] == position {
			return
		}
		positions = append(positions, position)
	}

	for index := 0; index < len(input.Stops)-1; index++ {
		stop := input.Stops[index]
		if stop.PathToNext == nil || len(stop.PathToNext.Positions) < 2 {
			appendPosition(stop.Position)
			appendPosition(input.Stops[index+1].Position)
			continue
		}
		for _, position := range stop.PathToNext.Positions {
			appendPosition(position)
		}
	}
	return route.LineString{Positions: positions}
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
