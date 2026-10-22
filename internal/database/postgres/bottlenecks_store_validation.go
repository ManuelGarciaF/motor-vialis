package postgres

import (
	"fmt"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
)

// Final passages must retain valid temporal support within the closed sequence.
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
