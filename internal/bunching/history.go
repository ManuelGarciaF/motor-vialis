package bunching

import (
	"context"
	"fmt"
	"time"
)

type HistoryPolicy struct {
	DefaultWindow, MaximumWindow time.Duration
	DefaultLimit, MaximumLimit   int
}

func (p HistoryPolicy) Validate() error {
	if p.DefaultWindow <= 0 || p.MaximumWindow < p.DefaultWindow || p.DefaultLimit < 1 || p.MaximumLimit < p.DefaultLimit {
		return fmt.Errorf("invalid bunching history policy")
	}
	return nil
}

type HistoryQuery struct {
	From, To      *time.Time
	State         string
	Limit, Offset int
}
type HistoryRequest struct {
	From, To      time.Time
	State         string
	Limit, Offset int
}
type QueryError struct{ Field, Message string }

func (e *QueryError) Error() string { return e.Field + ": " + e.Message }

func NormalizeHistory(q HistoryQuery, now time.Time, policy HistoryPolicy, futureSkew time.Duration) (HistoryRequest, error) {
	r := HistoryRequest{To: now, State: q.State, Limit: q.Limit, Offset: q.Offset}
	if err := policy.Validate(); err != nil {
		return r, err
	}
	if q.To != nil {
		r.To = *q.To
	}
	r.From = r.To.Add(-policy.DefaultWindow)
	if q.From != nil {
		r.From = *q.From
	}
	if !r.From.Before(r.To) {
		return r, &QueryError{Field: "from", Message: "must precede to"}
	}
	if r.To.Sub(r.From) > policy.MaximumWindow {
		return r, &QueryError{Field: "from", Message: "requested window exceeds the maximum allowed duration"}
	}
	if r.To.After(now.Add(futureSkew)) {
		return r, &QueryError{Field: "to", Message: "must not be in the future"}
	}
	if r.State == "" {
		r.State = "all"
	}
	if r.State != "all" && r.State != "open" && r.State != "closed" {
		return r, &QueryError{Field: "state", Message: "must be all, open or closed"}
	}
	if r.Limit == 0 {
		r.Limit = policy.DefaultLimit
	}
	if r.Limit < 1 || r.Limit > policy.MaximumLimit {
		return r, &QueryError{Field: "limit", Message: "must be within the allowed range"}
	}
	if r.Offset < 0 {
		return r, &QueryError{Field: "offset", Message: "must be non-negative"}
	}
	return r, nil
}

type HistoricalEvent struct {
	Event
	LineID              *int64     `json:"lineId"`
	State               string     `json:"state"`
	Active              bool       `json:"active"`
	ClosedAt            *time.Time `json:"closedAt"`
	CloseReason         string     `json:"closeReason"`
	RepresentativePoint Point      `json:"representativePoint"`
	DurationSeconds     float64    `json:"durationSeconds"`
	ConfirmationPending bool       `json:"confirmationPending"`
}

type EventPage struct {
	From              time.Time         `json:"from"`
	To                time.Time         `json:"to"`
	State             string            `json:"state"`
	Total             int64             `json:"total"`
	Limit             int               `json:"limit"`
	Offset            int               `json:"offset"`
	LatestCaptureAt   *time.Time        `json:"latestCaptureAt"`
	CaptureAgeSeconds *float64          `json:"captureAgeSeconds"`
	Partial           bool              `json:"partial"`
	Stale             bool              `json:"stale"`
	Events            []HistoricalEvent `json:"events"`
}

func (s *Service) List(ctx context.Context, query HistoryQuery) (EventPage, error) {
	now := s.now()
	request, err := NormalizeHistory(query, now, s.historyPolicy, s.policy.MaximumFutureSkew)
	if err != nil {
		return EventPage{}, err
	}
	return s.repository.FindHistory(ctx, request, now, s.policy)
}
