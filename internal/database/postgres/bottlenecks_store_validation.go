package postgres

import (
	"fmt"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

// Capture completeness comes from H0011 metadata, not a producer-supplied flag.
func validateStoredAbsence(old bottlenecks.CaptureState, exists bool, next bottlenecks.CaptureState, measurement bottlenecks.CaptureMeasurement, partial bool) error {
	if !exists {
		if next.Watermark == nil || next.MissingCaptures != 0 || measurement.Closed || !next.ClosedThrough.IsZero() {
			return fmt.Errorf("invalid new stream")
		}
		return nil
	}

	updated := next.Watermark != nil && (old.Watermark == nil || next.Watermark.ObservedAt.After(old.Watermark.ObservedAt))
	expected := old.MissingCaptures
	closed := false
	closedThrough := old.ClosedThrough
	if updated {
		expected = 0
	} else if !partial && old.Watermark != nil && expected < bottlenecks.MissingCaptureLimit {
		expected++
		closed = expected == bottlenecks.MissingCaptureLimit
		if closed {
			closedThrough = old.Watermark.ObservedAt
		}
	}
	if next.MissingCaptures != expected || measurement.Closed != closed || !next.ClosedThrough.Equal(closedThrough) {
		return fmt.Errorf("transition disagrees with capture absence metadata")
	}
	return nil
}

func validateStoredMeasurement(layout bottlenecks.Layout, state bottlenecks.CaptureState, m bottlenecks.CaptureMeasurement) error {
	if m.Duplicates < 0 || m.LateObservations < 0 {
		return fmt.Errorf("invalid measurement counters")
	}
	var through time.Time
	if state.Watermark != nil {
		through = state.Watermark.ObservedAt
	}
	if err := validateMeasurementSupport(layout, state.VehicleID, state.Policy, through, m.Measurements); err != nil {
		return err
	}
	if m.Closed && (state.MissingCaptures != bottlenecks.MissingCaptureLimit || state.Previous != nil || state.Pending != nil) {
		return fmt.Errorf("invalid stream closure")
	}
	return nil
}

func validateMeasurementSupport(layout bottlenecks.Layout, vehicleID string, policy bottlenecks.MeasurementPolicy, through time.Time, m bottlenecks.Measurements) error {
	if m.Duplicates < 0 {
		return fmt.Errorf("invalid measurement counters")
	}

	for _, count := range m.Rejections {
		if count < 0 {
			return fmt.Errorf("invalid rejection counter")
		}
	}

	sectors := layout.Sectors()
	validate := func(vehicle string, scope bottlenecks.Scope, sector bottlenecks.Sector, c bottlenecks.Crossing) error {
		if vehicle != vehicleID ||
			scope.Version != layout.Version() ||
			scope.ProviderRouteID == "" ||
			scope.ProviderDirection < 0 ||
			scope.ProviderDirection > 1 ||
			sector.Index < 0 ||
			sector.Index >= len(sectors) ||
			sectors[sector.Index] != sector {
			return fmt.Errorf("measurement identity/layout mismatch")
		}
		if c.At.IsZero() ||
			c.BeforeAt.IsZero() ||
			c.AfterAt.IsZero() ||
			c.At.Before(c.BeforeAt) ||
			c.At.After(c.AfterAt) ||
			c.AfterAt.Sub(c.BeforeAt) > policy.MaximumGap ||
			through.IsZero() ||
			c.AfterAt.After(through) {
			return fmt.Errorf("invalid crossing support")
		}
		return nil
	}
	for _, p := range m.Passages {
		if err := validate(p.VehicleID, p.Scope, p.Sector, p.Entered); err != nil {
			return err
		}
		if err := validate(p.VehicleID, p.Scope, p.Sector, p.Exited); err != nil {
			return err
		}

		expected := p.Sector.LengthMeters() / p.Duration.Seconds() * 3.6
		if p.Duration <= 0 ||
			p.Duration != p.Exited.At.Sub(p.Entered.At) ||
			p.SpeedKPH != expected ||
			p.InteriorObservations < 0 ||
			p.MaximumSupportingGap < 0 ||
			p.MaximumSupportingGap > policy.MaximumGap ||
			p.MaximumSupportingGap < p.Entered.AfterAt.Sub(p.Entered.BeforeAt) ||
			p.MaximumSupportingGap < p.Exited.AfterAt.Sub(p.Exited.BeforeAt) {
			return fmt.Errorf("invalid passage quality")
		}

		same := p.Entered.BeforeAt.Equal(p.Exited.BeforeAt) && p.Entered.AfterAt.Equal(p.Exited.AfterAt)
		if p.SameGPSInterval != same {
			return fmt.Errorf("invalid passage support category")
		}
	}

	for _, p := range m.Incomplete {
		if err := validate(p.VehicleID, p.Scope, p.Sector, p.Entered); err != nil {
			return err
		}
		if p.Reason == "" || p.Reason == bottlenecks.BatchBoundary {
			return fmt.Errorf("incremental entry has no final rejection reason")
		}
	}

	return nil
}
