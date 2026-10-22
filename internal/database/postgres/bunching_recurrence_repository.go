package postgres

import (
	"context"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
)

// FindRecurrence aggregates stored encounters, independently of current capture
// freshness. GTFS route/direction survives catalog ID and shape replacements.
func (r *BunchingRepository) FindRecurrence(ctx context.Context, id *int64, now time.Time, policy bunching.Policy, recurrence bunching.RecurrencePolicy) (bunching.RecurrencePage, error) {
	page := bunching.RecurrencePage{From: now.Add(-recurrence.Window), To: now, TimeZone: recurrence.TimeZone, MinimumDays: recurrence.MinimumDays, Lines: []bunching.LineRecurrence{}}
	if err := recurrence.Validate(); err != nil {
		return page, err
	}

	tx, err := r.database.Begin(ctx)
	if err != nil {
		return page, err
	}
	defer tx.Rollback(context.Background())
	// Clip evidence to [from,to), then expand its local hours. Endpoints are
	// half-open: an encounter ending exactly at 09:00 does not occupy 09–10.
	// ClosedAt is deliberately ignored: missing/expired closures cannot extend
	// the time for which proximity was observed. Duration qualification uses the
	// complete encounter, not the clipped fragment.
	rows, err := tx.Query(ctx, r.sql(findBunchingRecurrenceSQL),
		page.From, page.To, recurrence.TimeZone, policy.MinimumObservations,
		policy.MinimumDuration.Seconds(), id, recurrence.MinimumDays, policy.MinimumSpeedMetersPerSecond,
	)
	if err != nil {
		return page, err
	}

	for rows.Next() {
		line := bunching.LineRecurrence{RecurrenceSummary: bunching.RecurrenceSummary{From: page.From, To: page.To, TimeZone: page.TimeZone, MinimumDays: page.MinimumDays}}
		if err := rows.Scan(&line.LineID, &line.GTFSRouteID, &line.Direction, &line.Line, &line.Branch, &line.Mapped, &line.EventCount, &line.Dates, &line.Hours); err != nil {
			rows.Close()
			return page, err
		}

		line.DaysWithEvents = len(line.Dates)
		for _, hour := range line.Hours {
			line.Recurrent = line.Recurrent || hour.Recurrent
		}

		page.Lines = append(page.Lines, line)
	}

	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	return page, tx.Commit(ctx)
}
