package gcba

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
)

type rawPosition struct {
	RouteID        *string  `json:"route_id"`
	Latitude       *float64 `json:"latitude"`
	Longitude      *float64 `json:"longitude"`
	Speed          *float64 `json:"speed"`
	Timestamp      *int64   `json:"timestamp"`
	VehicleID      *string  `json:"id"`
	Direction      *int16   `json:"direction"`
	AgencyName     string   `json:"agency_name"`
	AgencyID       int64    `json:"agency_id"`
	RouteShortName string   `json:"route_short_name"`
	ProviderTripID string   `json:"tip_id"`
	TripHeadsign   string   `json:"trip_headsign"`
}

func decode(body []byte, receivedAt time.Time, maximumAge, maximumFutureSkew time.Duration) (bunching.Capture, error) {
	if trimmed := bytes.TrimSpace(body); len(trimmed) == 0 || trimmed[0] != '[' {
		return bunching.Capture{}, &Error{Code: ErrorInvalidResponse, Message: "GCBA response is not a JSON array"}
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var rows []json.RawMessage
	if err := decoder.Decode(&rows); err != nil {
		return bunching.Capture{}, &Error{
			Code:    ErrorInvalidResponse,
			Message: "GCBA response is not a JSON array",
			Cause:   err,
		}
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return bunching.Capture{}, &Error{Code: ErrorInvalidResponse, Message: "GCBA response has trailing JSON values"}
	}

	capture := bunching.Capture{
		ReceivedAt: receivedAt,
		Positions:  make([]bunching.Position, 0, len(rows)),
		Stats:      bunching.CaptureStats{TotalRows: len(rows)},
	}
	seen := make(map[string]struct{}, len(rows))
	for _, encoded := range rows {
		var raw rawPosition
		if err := json.Unmarshal(encoded, &raw); err != nil {
			capture.Stats.InvalidRows++
			continue
		}
		position, err := validate(raw, receivedAt, maximumAge, maximumFutureSkew)
		if err != nil {
			capture.Stats.InvalidRows++
			continue
		}
		key := fmt.Sprintf("%s\x00%d", position.VehicleID, position.ObservedAt.Unix())
		if _, exists := seen[key]; exists {
			capture.Stats.DuplicateRows++
			continue
		}
		seen[key] = struct{}{}
		if !position.Fresh {
			capture.Stats.StaleRows++
		}
		capture.Positions = append(capture.Positions, position)
	}
	capture.Stats.ValidRows = len(capture.Positions)
	return capture, nil
}

func validate(raw rawPosition, receivedAt time.Time, maximumAge, maximumFutureSkew time.Duration) (bunching.Position, error) {
	if raw.RouteID == nil || strings.TrimSpace(*raw.RouteID) == "" {
		return bunching.Position{}, fmt.Errorf("route_id is required")
	}
	if raw.VehicleID == nil || strings.TrimSpace(*raw.VehicleID) == "" {
		return bunching.Position{}, fmt.Errorf("vehicle id is required")
	}
	if raw.Direction == nil || (*raw.Direction != 0 && *raw.Direction != 1) {
		return bunching.Position{}, fmt.Errorf("direction must be 0 or 1")
	}
	if raw.Latitude == nil || *raw.Latitude < -90 || *raw.Latitude > 90 {
		return bunching.Position{}, fmt.Errorf("latitude is invalid")
	}
	if raw.Longitude == nil || *raw.Longitude < -180 || *raw.Longitude > 180 {
		return bunching.Position{}, fmt.Errorf("longitude is invalid")
	}
	if raw.Speed == nil || *raw.Speed < 0 {
		return bunching.Position{}, fmt.Errorf("speed is invalid")
	}
	if raw.Timestamp == nil || *raw.Timestamp <= 0 {
		return bunching.Position{}, fmt.Errorf("timestamp is invalid")
	}
	observedAt := time.Unix(*raw.Timestamp, 0).UTC()
	age := receivedAt.Sub(observedAt)
	if age < -maximumFutureSkew {
		return bunching.Position{}, fmt.Errorf("timestamp is too far in the future")
	}
	return bunching.Position{
		VehicleID:            *raw.VehicleID,
		ProviderRouteID:      *raw.RouteID,
		ProviderDirection:    *raw.Direction,
		ProviderTripID:       raw.ProviderTripID,
		RouteShortName:       raw.RouteShortName,
		TripHeadsign:         raw.TripHeadsign,
		AgencyID:             raw.AgencyID,
		AgencyName:           raw.AgencyName,
		Latitude:             *raw.Latitude,
		Longitude:            *raw.Longitude,
		SpeedMetersPerSecond: *raw.Speed,
		ObservedAt:           observedAt,
		Fresh:                age <= maximumAge,
	}, nil
}
