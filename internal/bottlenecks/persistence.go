package bottlenecks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrModelChanged       = errors.New("persisted measurement model differs")
	ErrCheckpointConflict = errors.New("measurement checkpoint changed")
	ErrCaptureOutOfOrder  = errors.New("capture is not next after checkpoint")
	ErrModelNotFound      = errors.New("measurement model not activated")
	ErrSourceExpired      = errors.New("measurement checkpoint precedes raw retention; explicit recovery required")
)

// PersistenceModel identifies an explicit experimental policy. Names cannot be
// reused with another algorithm version or policy. There are no runtime defaults.
type PersistenceModel struct {
	Name               string
	MeasurementVersion string
	MatcherVersion     string
	NominalMeters      float64
	Policy             MeasurementPolicy
	TimeZone           string
	RawRetentionDays   int
	ClosureCaptures    int
}

func (m PersistenceModel) Validate() error {
	if strings.TrimSpace(m.Name) == "" ||
		strings.TrimSpace(m.MeasurementVersion) == "" ||
		strings.TrimSpace(m.MatcherVersion) == "" ||
		!finite(m.NominalMeters) ||
		meters(m.NominalMeters) <= 0 ||
		m.NominalMeters > 1e9 ||
		m.TimeZone != "America/Argentina/Buenos_Aires" ||
		m.RawRetentionDays < 1 ||
		m.RawRetentionDays > 31 ||
		m.ClosureCaptures != MissingCaptureLimit {
		return fmt.Errorf("invalid persistence model")
	}
	if _, err := time.LoadLocation(m.TimeZone); err != nil {
		return err
	}
	return m.Policy.validate()
}

type ProcessingRun struct {
	Model                           PersistenceModel
	ActivatedAt                     time.Time
	LastReceivedAt                  *time.Time
	ActivationCaptureID, Checkpoint int64
}

type PersistedLayout struct {
	ID     int64
	Layout Layout
}

// StreamTransition is produced by the domain's MeasureCapture, not HTTP input.
// PreviousCaptureID is zero for a new stream; otherwise it is the stored stream
// checkpoint. Every stored stream must participate in a capture commit, including
// absences. Matching and version-change barriers remain the producer's concern.
type StreamTransition struct {
	LayoutID          int64
	PreviousCaptureID int64
	State             CaptureState
	Measurement       CaptureMeasurement
}

type PersistedStream struct {
	LayoutID int64
	State    CaptureState
}

type CaptureCommit struct {
	Checkpoint                                                        int64
	Passages, Incomplete, LateObservations, Duplicates, ClosedStreams int
	Rejections                                                        map[Rejection]int
}

// PassageStore commits measurements, every tracked stream's restart state and
// the global checkpoint atomically. Catalog snapshots outlive GTFS reloads and
// raw purges. Activating an existing model never resets its start/checkpoint.
type PassageStore interface {
	Activate(context.Context, PersistenceModel) (ProcessingRun, error)
	RegisterLayout(context.Context, string, int64) (PersistedLayout, error)
	LoadStates(context.Context, string) ([]PersistedStream, error)
	CommitCapture(context.Context, string, int64, int64, []StreamTransition) (CaptureCommit, error)
}

// Validate exposes restart-state checks at persistence boundaries, without
// performing a transition or emitting batch-boundary censoring.
func (s CaptureState) Validate(layout Layout) error { return validateCaptureState(layout, s) }
