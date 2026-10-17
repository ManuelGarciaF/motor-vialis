package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
)

// A record is one finalized supported entry, complete or incomplete. Scope,
// sector and exact UTC entry identify evidence independently of its outcome.
type bottleneckMeasurementRecord struct {
	layoutID, captureID int64
	vehicle             string
	scope               bottlenecks.Scope
	sector              bottlenecks.Sector
	entry               bottlenecks.Crossing
	complete            bool
	reason              string
	duration            time.Duration
	speedKPH            float64
	payload             any
}

func (s *BottlenecksStore) insertMeasurement(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, record bottleneckMeasurementRecord) error {
	// Canonical UTC nanosecond text prevents timestamptz's microsecond precision
	// from collapsing distinct entries. Completion/reason are not part of identity.
	key := struct {
		Layout  int64
		Vehicle string
		Scope   bottlenecks.Scope
		Sector  int
		Entry   string
	}{record.layoutID, record.vehicle, record.scope, record.sector.Index, record.entry.At.UTC().Format(time.RFC3339Nano)}
	identity, err := json.Marshal(key)
	if err != nil {
		return err
	}

	digest := sha256.Sum256(identity)
	data, err := json.Marshal(record.payload)
	if err != nil {
		return err
	}

	location, err := time.LoadLocation(run.Model.TimeZone)
	if err != nil {
		return err
	}

	local := record.entry.At.In(location)
	var why, ns, kph any
	if record.complete {
		ns = int64(record.duration)
		kph = record.speedKPH
	} else {
		why = record.reason
	}
	_, err = tx.Exec(ctx, s.sql(insertBottleneckMeasurementSQL),
		run.Model.Name, hex.EncodeToString(digest[:]), record.layoutID, record.sector.Index,
		record.vehicle, record.scope.ProviderRouteID, record.scope.ProviderDirection,
		record.captureID, record.entry.At, local.Format("2006-01-02"), local.Hour(),
		record.complete, why, ns, kph, data)
	return err
}
