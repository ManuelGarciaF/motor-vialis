package bunching

import (
	"context"
	"fmt"
	"time"
)

// Recurrence counts encounters overlapping the same local clock hour on
// distinct dates, not samples or merely events on different days.
type RecurrencePolicy struct {
	Window      time.Duration
	MinimumDays int
	TimeZone    string
}

func (p RecurrencePolicy) Validate() error {
	if p.Window <= 0 || p.MinimumDays < 2 {
		return fmt.Errorf("invalid bunching recurrence policy")
	}
	if _, err := time.LoadLocation(p.TimeZone); err != nil || p.TimeZone == "" {
		return fmt.Errorf("invalid bunching recurrence timezone")
	}
	return nil
}

// HourRecurrence describes [Hour:00, (Hour+1):00) in the policy's timezone.
// Each event counts once per clock hour, even if it spans multiple dates.
type HourRecurrence struct {
	Hour           int      `json:"hour"`
	EventCount     int64    `json:"eventCount"`
	DaysWithEvents int      `json:"daysWithEvents"`
	Dates          []string `json:"dates"`
	Recurrent      bool     `json:"recurrent"`
}

type RecurrenceSummary struct {
	From           time.Time        `json:"from"`
	To             time.Time        `json:"to"`
	TimeZone       string           `json:"timeZone"`
	MinimumDays    int              `json:"minimumDays"`
	EventCount     int64            `json:"eventCount"`
	DaysWithEvents int              `json:"daysWithEvents"`
	Dates          []string         `json:"dates"`
	Recurrent      bool             `json:"recurrent"`
	Hours          []HourRecurrence `json:"hours"`
}

type LineRecurrence struct {
	LineID      *int64 `json:"lineId"`
	GTFSRouteID string `json:"gtfsRouteId"`
	Direction   int16  `json:"direction"`
	Line        string `json:"line"`
	Branch      string `json:"branch"`
	Mapped      bool   `json:"mapped"`
	RecurrenceSummary
}

type RecurrencePage struct {
	From        time.Time        `json:"from"`
	To          time.Time        `json:"to"`
	TimeZone    string           `json:"timeZone"`
	MinimumDays int              `json:"minimumDays"`
	Lines       []LineRecurrence `json:"lines"`
}

func (s *Service) Recurrence(ctx context.Context) (RecurrencePage, error) {
	return s.repository.FindRecurrence(ctx, nil, s.now(), s.policy, s.recurrencePolicy)
}
