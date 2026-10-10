package bunching

import (
	"context"
	"time"
)

type ActivePair struct {
	Event
	RepresentativePoint Point   `json:"representativePoint"`
	DurationSeconds     float64 `json:"durationSeconds"`
	ConfirmationPending bool    `json:"confirmationPending"`
}

type Current struct {
	LineID            int64        `json:"lineId"`
	Line              string       `json:"line"`
	Branch            string       `json:"branch"`
	Direction         int16        `json:"direction"`
	Mapped            bool         `json:"mapped"`
	LatestCaptureAt   *time.Time   `json:"latestCaptureAt"`
	CaptureAgeSeconds *float64     `json:"captureAgeSeconds"`
	Partial           bool         `json:"partial"`
	Stale             bool         `json:"stale"`
	Pairs             []ActivePair `json:"pairs"`
}

type CurrentRepository interface {
	FindCurrent(context.Context, int64, time.Time, Policy) (Current, error)
	FindHistory(context.Context, HistoryRequest, time.Time, Policy) (EventPage, error)
}
type Service struct {
	repository    CurrentRepository
	policy        Policy
	historyPolicy HistoryPolicy
	now           func() time.Time
}

func NewService(repository CurrentRepository, policy Policy, historyPolicy HistoryPolicy) (*Service, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if err := historyPolicy.Validate(); err != nil {
		return nil, err
	}
	return &Service{repository: repository, policy: policy, historyPolicy: historyPolicy, now: time.Now}, nil
}
func (s *Service) Get(ctx context.Context, id int64) (Current, error) {
	return s.repository.FindCurrent(ctx, id, s.now(), s.policy)
}
