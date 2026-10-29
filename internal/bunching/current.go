package bunching

import (
	"context"
	"fmt"
	"time"
)

type ActivePair struct {
	Event
	RepresentativePoint Point   `json:"representativePoint"`
	DurationSeconds     float64 `json:"durationSeconds"`
	ConfirmationPending bool    `json:"confirmationPending"`
}

type Current struct {
	LineID            int64             `json:"lineId"`
	Line              string            `json:"line"`
	Branch            string            `json:"branch"`
	Direction         int16             `json:"direction"`
	Mapped            bool              `json:"mapped"`
	LatestCaptureAt   *time.Time        `json:"latestCaptureAt"`
	CaptureAgeSeconds *float64          `json:"captureAgeSeconds"`
	Partial           bool              `json:"partial"`
	Stale             bool              `json:"stale"`
	Pairs             []ActivePair      `json:"pairs"`
	Recurrence        RecurrenceSummary `json:"recurrence"`
}

type CurrentRepository interface {
	FindCurrent(context.Context, int64, time.Time, Policy) (Current, error)
	FindHistory(context.Context, HistoryRequest, time.Time, Policy) (EventPage, error)
	FindRecurrence(context.Context, *int64, time.Time, Policy, RecurrencePolicy) (RecurrencePage, error)
}
type Service struct {
	repository       CurrentRepository
	policy           Policy
	historyPolicy    HistoryPolicy
	recurrencePolicy RecurrencePolicy
	now              func() time.Time
}

func NewService(repository CurrentRepository, policy Policy, historyPolicy HistoryPolicy, recurrencePolicy RecurrencePolicy) (*Service, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if err := historyPolicy.Validate(); err != nil {
		return nil, err
	}
	if err := recurrencePolicy.Validate(); err != nil {
		return nil, err
	}
	return &Service{repository: repository, policy: policy, historyPolicy: historyPolicy, recurrencePolicy: recurrencePolicy, now: time.Now}, nil
}

// WithFixedTime freezes the API clock for demos of the last persisted worker
// state. Configure before serving requests; this does not replay older states.
func (s *Service) WithFixedTime(at time.Time) *Service {
	s.now = func() time.Time { return at }
	return s
}

func (s *Service) Get(ctx context.Context, id int64) (Current, error) {
	now := s.now()
	current, err := s.repository.FindCurrent(ctx, id, now, s.policy)
	if err != nil {
		return current, err
	}

	page, err := s.repository.FindRecurrence(ctx, &id, now, s.policy, s.recurrencePolicy)
	if err != nil {
		return current, err
	}
	if len(page.Lines) != 1 {
		return current, fmt.Errorf("missing bunching recurrence summary")
	}

	current.Recurrence = page.Lines[0].RecurrenceSummary
	return current, nil
}
