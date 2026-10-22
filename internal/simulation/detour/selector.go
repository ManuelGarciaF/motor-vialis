package detour

import (
	"fmt"
	"math"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
)

// pgrouting can split the same physical path at a virtual stop, changing the
// order of floating-point additions. Differences below one nanosecond are
// numerical noise, not a meaningful traffic-time advantage.
const travelTimeTieToleranceSeconds = 1e-9

type selectionState struct {
	kept        []int
	connections []Connection
	seconds     float64
}

// Select resolves the ordered stop DAG according to the requested
// lexicographic criterion. Required stops can never be skipped and forced stops
// can never be retained.
func Select(
	stops []ClassifiedStop,
	connections []Connection,
	criterion Criterion,
) (Selection, error) {
	if err := validateCriterion(criterion); err != nil {
		return Selection{}, err
	}
	if err := validateSelectorInput(stops, connections); err != nil {
		return Selection{}, err
	}

	incoming := make(map[int][]Connection)
	for _, connection := range connections {
		incoming[connection.ToStopOrder] = append(
			incoming[connection.ToStopOrder],
			cloneConnection(connection),
		)
	}

	// Keep separate states for paths with one retained stop and paths with at
	// least two. Without that dimension, a zero-cost one-stop prefix could
	// discard the only state that is itself a valid route.
	bestByEnd := make([][2]*selectionState, len(stops))
	for end := range stops {
		if stops[end].Availability == StopForcedUnavailable {
			continue
		}
		if !containsRequired(stops[:end]) {
			bestByEnd[end][0] = &selectionState{kept: []int{end}}
		}

		for _, connection := range incoming[end] {
			start := connection.FromStopOrder
			if containsRequired(stops[start+1 : end]) {
				continue
			}

			for category, previous := range bestByEnd[start] {
				if previous == nil {
					continue
				}
				// Conservar esta parada no puede obligar al trazado a darse
				// vuelta sobre ella: eso es un giro en U (aunque use la calzada
				// opuesta de una avenida dividida, que es otra arista y por eso
				// el filtro de aristas no lo ve). Nunca es legal para un bondi;
				// si la única forma de tocar la parada es volver, se omite.
				if reversesAtJunction(previous, connection) {
					continue
				}

				candidate := &selectionState{
					kept:        appendCopy(previous.kept, end),
					connections: appendConnection(previous.connections, connection),
					seconds:     previous.seconds + connection.TravelSeconds,
				}
				candidateCategory := min(1, category+1)
				current := bestByEnd[end][candidateCategory]
				if current == nil || statePrecedes(
					candidate,
					current,
					criterion,
					end+1,
				) {
					bestByEnd[end][candidateCategory] = candidate
				}
			}
		}
	}

	var best *selectionState
	for end, states := range bestByEnd {
		candidate := states[1]
		if candidate == nil || containsRequired(stops[end+1:]) {
			continue
		}
		if best == nil || statePrecedes(
			candidate,
			best,
			criterion,
			len(stops),
		) {
			best = candidate
		}
	}

	if best == nil {
		return Selection{}, &Error{
			Code:    ErrorNoDetourWithinSearchArea,
			Kind:    ErrorKindNoSolution,
			Message: "no path retains every required stop and at least two stops total",
		}
	}

	keptSet := make(map[int]struct{}, len(best.kept))
	for _, order := range best.kept {
		keptSet[order] = struct{}{}
	}

	omitted := make([]int, 0, len(stops)-len(best.kept))
	for order := range stops {
		if _, retained := keptSet[order]; !retained {
			omitted = append(omitted, order)
		}
	}

	return Selection{
		KeptStopOrders:    append([]int(nil), best.kept...),
		OmittedStopOrders: omitted,
		TravelSeconds:     best.seconds,
		Connections:       cloneConnections(best.connections),
	}, nil
}

// reversalMinimumAngleDegrees: si el rumbo de marcha se invierte más que esto al
// pasar por una parada, el trazado está volviendo sobre sí mismo (un giro en U
// en la parada). 150° deja pasar giros cerrados reales de esquina y sólo frena
// la vuelta franca.
const reversalMinimumAngleDegrees = 150.0

// reversesAtJunction informa si encadenar la conexión `next` después de `previous`
// hace que el trazado se dé vuelta en la parada que comparten.
func reversesAtJunction(previous *selectionState, next Connection) bool {
	if len(previous.connections) == 0 {
		return false
	}

	last := previous.connections[len(previous.connections)-1]
	arriving, ok1 := arrivingBearing(last.Path)
	leaving, ok2 := leavingBearing(next.Path)
	if !ok1 || !ok2 {
		return false
	}
	return angularDifference(arriving, leaving) >= reversalMinimumAngleDegrees
}

// bearingBaselineMeters: distancia sobre la que se mide el rumbo de marcha en
// cada punta, para que sea estable y no lo domine el ruido vértice a vértice.
const bearingBaselineMeters = 20.0

// Cada conexión arranca y termina con un "piolín" parada→calle (el primer y
// último segmento: la parada y su proyección sobre la calzada). Ese stub apunta
// de costado, no en el sentido de marcha, así que se saltea: el rumbo se toma
// desde el segundo vértice (leaving) y hasta el anteúltimo (arriving), sobre una
// base de ~20 m.
func leavingBearing(path route.LineString) (float64, bool) {
	p := path.Positions
	if len(p) < 3 {
		return rawLeavingBearing(p)
	}

	base := p[1]       // saltea el stub de origen (p[0]→p[1])
	last := len(p) - 2 // saltea el stub de destino (p[n-2]→p[n-1])
	acc := 0.0
	for i := 2; i <= last; i++ {
		acc += metersBetween(p[i-1], p[i])
		if acc >= bearingBaselineMeters {
			return bearingBetween(base, p[i])
		}
	}

	return bearingBetween(base, p[last])
}

func arrivingBearing(path route.LineString) (float64, bool) {
	p := path.Positions
	if len(p) < 3 {
		return rawArrivingBearing(p)
	}

	end := p[len(p)-2] // saltea el stub de destino
	first := 1         // saltea el stub de origen
	acc := 0.0
	for i := len(p) - 3; i >= first; i-- {
		acc += metersBetween(p[i], p[i+1])
		if acc >= bearingBaselineMeters {
			return bearingBetween(p[i], end)
		}
	}

	return bearingBetween(p[first], end)
}

// Caminos muy cortos (sin tramo de calzada entre los stubs): se usa punta a punta.
func rawLeavingBearing(p []route.Position) (float64, bool) {
	for i := 1; i < len(p); i++ {
		if bearing, ok := bearingBetween(p[0], p[i]); ok {
			return bearing, true
		}
	}

	return 0, false
}

func rawArrivingBearing(p []route.Position) (float64, bool) {
	for i := len(p) - 2; i >= 0; i-- {
		if bearing, ok := bearingBetween(p[i], p[len(p)-1]); ok {
			return bearing, true
		}
	}

	return 0, false
}

func metersBetween(a, b route.Position) float64 {
	const metersPerDegree = 111_320.0
	north := (b.Latitude - a.Latitude) * metersPerDegree
	east := (b.Longitude - a.Longitude) * metersPerDegree * math.Cos(math.Pi/180*(a.Latitude+b.Latitude)/2)
	return math.Hypot(north, east)
}

// bearingBetween devuelve el rumbo de marcha a→b en grados [0,360), con una
// corrección por latitud para que el ángulo sea real a escala urbana. ok es
// falso cuando los dos puntos coinciden.
func bearingBetween(a, b route.Position) (float64, bool) {
	north := b.Latitude - a.Latitude
	east := (b.Longitude - a.Longitude) * math.Cos(math.Pi/180*(a.Latitude+b.Latitude)/2)
	if math.Abs(north) < 1e-12 && math.Abs(east) < 1e-12 {
		return 0, false
	}

	degrees := math.Atan2(east, north) * 180 / math.Pi
	if degrees < 0 {
		degrees += 360
	}
	return degrees, true
}

// angularDifference es la diferencia mínima entre dos rumbos, en [0,180].
func angularDifference(a, b float64) float64 {
	diff := math.Mod(math.Abs(a-b), 360)
	if diff > 180 {
		diff = 360 - diff
	}
	return diff
}

func validateCriterion(criterion Criterion) error {
	if criterion != CriterionShortestTime && criterion != CriterionFewestLostStops {
		return &ValidationError{
			Field:   "criterion",
			Message: "must be MENOR_TIEMPO or MENOR_PARADAS_PERDIDAS",
		}
	}
	return nil
}

func validateSelectorInput(stops []ClassifiedStop, connections []Connection) error {
	for index, stop := range stops {
		if stop.Order != index {
			return fmt.Errorf("select detour: stop order %d at position %d", stop.Order, index)
		}

		switch stop.Availability {
		case StopForcedUnavailable, StopOptional, StopRequired:
		default:
			return fmt.Errorf("select detour: stop %d has invalid availability %q", index, stop.Availability)
		}
	}

	for _, connection := range connections {
		if connection.FromStopOrder < 0 ||
			connection.ToStopOrder >= len(stops) ||
			connection.FromStopOrder >= connection.ToStopOrder {
			return fmt.Errorf(
				"select detour: invalid connection %d -> %d",
				connection.FromStopOrder,
				connection.ToStopOrder,
			)
		}
		if connection.TravelSeconds < 0 ||
			math.IsNaN(connection.TravelSeconds) ||
			math.IsInf(connection.TravelSeconds, 0) {
			return fmt.Errorf(
				"select detour: connection %d -> %d has invalid travel time",
				connection.FromStopOrder,
				connection.ToStopOrder,
			)
		}
	}

	return nil
}

func containsRequired(stops []ClassifiedStop) bool {
	for _, stop := range stops {
		if stop.Availability == StopRequired {
			return true
		}
	}

	return false
}

func statePrecedes(
	candidate, current *selectionState,
	criterion Criterion,
	consideredStops int,
) bool {
	candidateOmitted := consideredStops - len(candidate.kept)
	currentOmitted := consideredStops - len(current.kept)
	secondsDiffer := math.Abs(candidate.seconds-current.seconds) >
		travelTimeTieToleranceSeconds
	if criterion == CriterionShortestTime {
		if secondsDiffer {
			return candidate.seconds < current.seconds
		}
		if candidateOmitted != currentOmitted {
			return candidateOmitted < currentOmitted
		}
	} else {
		if candidateOmitted != currentOmitted {
			return candidateOmitted < currentOmitted
		}
		if secondsDiffer {
			return candidate.seconds < current.seconds
		}
	}
	if comparison := compareInts(candidate.kept, current.kept); comparison != 0 {
		return comparison < 0
	}
	return compareInts64(
		flattenEdgeIDs(candidate.connections),
		flattenEdgeIDs(current.connections),
	) < 0
}

func compareInts(left, right []int) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}

	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func compareInts64(left, right []int64) int {
	for index := 0; index < min(len(left), len(right)); index++ {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}

	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func flattenEdgeIDs(connections []Connection) []int64 {
	var result []int64
	for _, connection := range connections {
		result = append(result, connection.EdgeIDs...)
	}

	return result
}

func appendCopy(values []int, value int) []int {
	result := make([]int, len(values), len(values)+1)
	copy(result, values)
	return append(result, value)
}

func appendConnection(values []Connection, value Connection) []Connection {
	result := cloneConnections(values)
	return append(result, cloneConnection(value))
}

func cloneConnections(values []Connection) []Connection {
	result := make([]Connection, len(values))
	for index, value := range values {
		result[index] = cloneConnection(value)
	}

	return result
}

func cloneConnection(value Connection) Connection {
	value.EdgeIDs = append([]int64(nil), value.EdgeIDs...)
	value.Path.Positions = append([]route.Position(nil), value.Path.Positions...)
	return value
}
