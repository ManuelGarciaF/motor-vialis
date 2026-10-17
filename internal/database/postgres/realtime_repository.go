package postgres

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type realtimeBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// RealtimeRepository stores only GCBA identities with a current GTFS mapping.
// No foreign key to the replaceable GTFS catalog is retained in raw data.
type RealtimeRepository struct {
	database       realtimeBeginner
	retentionDays  int
	schema         string
	bunchingPolicy *bunching.Policy
}

var _ bunching.Repository = (*RealtimeRepository)(nil)

func NewRealtimeRepository(pool *pgxpool.Pool, retentionDays int) (*RealtimeRepository, error) {
	if pool == nil || retentionDays < 1 || retentionDays > 31 {
		return nil, fmt.Errorf("invalid realtime repository configuration")
	}
	return &RealtimeRepository{database: pool, retentionDays: retentionDays, schema: "vialis"}, nil
}

func (r *RealtimeRepository) sql(query string) string {
	return queryInSchema(query, r.schema)
}
func utcDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

const realtimeLock int64 = 110011

// MaintainPartitions keeps today and the previous retentionDays-1 UTC dates,
// and precreates tomorrow. Expiration drops whole raw partitions, never rows.
func (r *RealtimeRepository) MaintainPartitions(ctx context.Context, now time.Time) (bunching.MaintenanceResult, error) {
	result := bunching.MaintenanceResult{}
	if now.IsZero() {
		return result, fmt.Errorf("maintenance time is required")
	}

	tx, err := r.database.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())

	if _, err = tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock($1)
	`, realtimeLock); err != nil {
		return result, err
	}

	today := utcDay(now)
	cutoff := today.AddDate(0, 0, 1-r.retentionDays)
	rows, err := tx.Query(ctx, listRealtimePartitionsSQL, r.schema)
	if err != nil {
		return result, err
	}

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return result, err
		}

		names = append(names, name)
	}

	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}

	pattern := regexp.MustCompile(`^posiciones_colectivos_raw_([0-9]{8})$`)
	for _, name := range names {
		parts := pattern.FindStringSubmatch(name)
		if parts == nil {
			continue
		}

		day, err := time.Parse("20060102", parts[1])
		if err != nil {
			return result, err
		}
		if day.Before(cutoff) {
			if _, err = tx.Exec(ctx, `
				DROP TABLE
			`+pgx.Identifier{r.schema, name}.Sanitize()); err != nil {
				return result, err
			}

			result.Dropped = append(result.Dropped, name)
		}
	}

	for day := cutoff; !day.After(today.AddDate(0, 0, 1)); day = day.AddDate(0, 0, 1) {
		name, created, err := r.ensurePartition(ctx, tx, day)
		if err != nil {
			return result, err
		}
		if created {
			result.Created = append(result.Created, name)
		}
	}

	_, err = tx.Exec(ctx, r.sql(deleteExpiredRealtimeCapturesSQL), cutoff)
	if err != nil {
		return result, err
	}

	err = tx.Commit(ctx)
	return result, err
}

func (r *RealtimeRepository) ensurePartition(ctx context.Context, tx pgx.Tx, day time.Time) (string, bool, error) {
	name := "posiciones_colectivos_raw_" + day.Format("20060102")
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2)
	`, r.schema, name).Scan(&exists)
	if err != nil || exists {
		return name, false, err
	}

	query := fmt.Sprintf(`
		CREATE TABLE %s PARTITION OF %s FOR
		VALUES
		FROM('%s') TO(
		    '%s'
		)
	`, pgx.Identifier{r.schema, name}.Sanitize(), pgx.Identifier{r.schema, "posiciones_colectivos_raw"}.Sanitize(), day.Format("2006-01-02"), day.AddDate(0, 0, 1).Format("2006-01-02"))
	_, err = tx.Exec(ctx, query)
	return name, err == nil, err
}

// StoreCapture resolves against the versioned equivalence table, COPYs a batch
// into temporary staging and inserts idempotently in one transaction. Fresh is
// not persisted: it is relative to a clock, not a permanent fact about a row.
func (r *RealtimeRepository) StoreCapture(ctx context.Context, capture bunching.Capture) (bunching.StoreResult, error) {
	result := bunching.StoreResult{}
	if capture.ReceivedAt.IsZero() {
		return result, fmt.Errorf("capture reception time is required")
	}

	tx, err := r.database.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(context.Background())

	if _, err = tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock($1)
	`, realtimeLock); err != nil {
		return result, err
	}

	today := utcDay(capture.ReceivedAt)
	cutoff := today.AddDate(0, 0, 1-r.retentionDays)
	batch := make([][]any, 0, len(capture.Positions))
	days := map[time.Time]bool{}
	for _, p := range capture.Positions {
		if p.ObservedAt.IsZero() || p.ObservedAt.After(capture.ReceivedAt.Add(time.Minute)) {
			return result, fmt.Errorf("position timestamp is invalid")
		}

		day := utcDay(p.ObservedAt)
		if day.Before(cutoff) {
			result.OutsideRetention++
			continue
		}

		days[day] = true
		batch = append(batch, []any{day, p.ObservedAt, p.VehicleID, p.ProviderRouteID, p.ProviderDirection, p.Longitude, p.Latitude, p.SpeedMetersPerSecond})
	}

	for day := range days {
		if _, _, err = r.ensurePartition(ctx, tx, day); err != nil {
			return result, err
		}
	}

	err = tx.QueryRow(ctx, r.sql(insertRealtimeCaptureSQL), capture.ReceivedAt, capture.Stats.TotalRows, capture.Stats.ValidRows, capture.Stats.InvalidRows, capture.Stats.DuplicateRows, capture.Stats.StaleRows, capture.Partial).Scan(&result.CaptureID)
	if err != nil {
		return result, err
	}

	_, err = tx.Exec(ctx, createRealtimeBatchSQL)
	if err != nil {
		return result, err
	}

	_, err = tx.CopyFrom(ctx, pgx.Identifier{"realtime_positions_batch"}, []string{"dia", "observado", "vehiculo", "route_id", "direction", "lon", "lat", "speed"}, pgx.CopyFromRows(batch))
	if err != nil {
		return result, err
	}

	err = tx.QueryRow(ctx, r.sql(countUnmappedRealtimePositionsSQL)).Scan(&result.Unmapped)
	if err != nil {
		return result, err
	}

	tag, err := tx.Exec(ctx, r.sql(insertRealtimePositionsSQL), capture.ReceivedAt, result.CaptureID)
	if err != nil {
		return result, err
	}

	result.Inserted = int(tag.RowsAffected())
	if r.bunchingPolicy != nil {
		if err = r.advanceBunching(ctx, tx, capture, &result); err != nil {
			return result, err
		}
	}
	// Allows repeated StoreCapture calls within an outer test transaction.
	if _, err = tx.Exec(ctx, `
		DROP TABLE realtime_positions_batch
	`); err != nil {
		return result, err
	}

	_, err = tx.Exec(ctx, r.sql(`
		UPDATE vialis.capturas_colectivos
		SET
		    filas_no_vinculadas = $2,
		    filas_insertadas = $3
		WHERE id_captura = $1
	`), result.CaptureID, result.Unmapped, result.Inserted)
	if err != nil {
		return result, err
	}

	err = tx.Commit(ctx)
	return result, err
}
