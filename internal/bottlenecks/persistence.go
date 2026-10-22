package bottlenecks

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrModelChanged  = errors.New("persisted measurement configuration differs")
	ErrModelNotFound = errors.New("measurement configuration not activated")
	ErrSourceExpired = errors.New("measurement checkpoint precedes raw retention; explicit recovery required")
)

// PersistenceModel records how passages were measured. It is immutable so stored
// evidence cannot silently mix policies or algorithm versions. Activation also
// establishes the source checkpoint; it does not backfill historical captures.
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
		return fmt.Errorf("invalid persistence configuration")
	}
	if _, err := time.LoadLocation(m.TimeZone); err != nil {
		return err
	}
	return m.Policy.validate()
}

// ProcessingRun is the durable configuration and source checkpoint, not a
// separately scheduled execution or processing mode.
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
