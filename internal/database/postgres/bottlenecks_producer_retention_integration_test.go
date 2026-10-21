package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The wrapper advances the retention clock and completes a real isolated purge
// immediately before source access. It reproduces midnight without wall-clock
// sleeps or clock injection into production code.
type producerMidnightBeginner struct {
	database         realtimeBeginner
	before, after    time.Time
	purge            func(context.Context) error
	nextCaptureQuery string
	purged, pinned   bool
	references       []time.Time
}

func (b *producerMidnightBeginner) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := b.database.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &producerMidnightTx{Tx: tx, clock: b}, nil
}

type producerMidnightTx struct {
	pgx.Tx
	clock *producerMidnightBeginner
}

func (tx *producerMidnightTx) purgeBeforeSource(ctx context.Context) error {
	if tx.clock.purged {
		return nil
	}
	if err := tx.clock.purge(ctx); err != nil {
		return err
	}
	tx.clock.purged = true
	return nil
}

func (tx *producerMidnightTx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	pin := strings.Contains(query, "LOCK TABLE") && strings.Contains(query, "posiciones_colectivos_raw") && strings.Contains(query, "IN ACCESS SHARE MODE")
	if pin {
		if err := tx.purgeBeforeSource(ctx); err != nil {
			return pgconn.CommandTag{}, err
		}
	}
	tag, err := tx.Tx.Exec(ctx, query, args...)
	if pin && err == nil {
		tx.clock.pinned = true
	}
	return tag, err
}

func (tx *producerMidnightTx) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	if query == checkBottleneckRetentionSQL {
		now := tx.clock.before
		if tx.clock.purged {
			now = tx.clock.after
		}
		tx.clock.references = append(tx.clock.references, args[0].(time.Time))
		return tx.Tx.QueryRow(ctx, `
			SELECT ($1::timestamptz AT TIME ZONE 'UTC')::date
				< ($3::timestamptz AT TIME ZONE 'UTC')::date - ($2::integer - 1)
		`, args[0], args[1], now)
	}
	// Without an explicit source pin, the same purge occurs before selecting the
	// next surviving capture. A fresh receipt must not conceal the lost capture.
	if query == tx.clock.nextCaptureQuery {
		if err := tx.purgeBeforeSource(ctx); err != nil {
			return producerMidnightErrorRow{err}
		}
	}
	return tx.Tx.QueryRow(ctx, query, args...)
}

type producerMidnightErrorRow struct{ err error }

func (r producerMidnightErrorRow) Scan(...any) error { return r.err }

func partitionProducerRetentionSource(t *testing.T, f *passageStoreFixture, oldest time.Time) (string, string) {
	t.Helper()
	oldName, freshName := "producer_raw_oldest", "producer_raw_fresh"
	f.exec(`
		ALTER TABLE vialis.posiciones_colectivos_raw RENAME TO producer_raw_template;
		CREATE TABLE vialis.posiciones_colectivos_raw
			(LIKE vialis.producer_raw_template INCLUDING ALL)
			PARTITION BY RANGE (dia_observado);
		DROP TABLE vialis.producer_raw_template;
	`)
	for _, partition := range []struct {
		name string
		day  time.Time
	}{{oldName, oldest}, {freshName, passageEpoch}} {
		query := fmt.Sprintf(`
			CREATE TABLE %s PARTITION OF %s
			FOR VALUES FROM ('%s') TO ('%s')
		`, pgx.Identifier{f.store.schema, partition.name}.Sanitize(), pgx.Identifier{f.store.schema, "posiciones_colectivos_raw"}.Sanitize(),
			partition.day.Format("2006-01-02"), partition.day.AddDate(0, 0, 1).Format("2006-01-02"))
		if _, err := f.pool.Exec(f.ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	return oldName, freshName
}

func TestProducerStoreMidnightPurgeDoesNotSkipOriginalReference(t *testing.T) {
	for _, lastReceipt := range []bool{false, true} {
		for _, survivingCapture := range []bool{false, true} {
			t.Run(fmt.Sprintf("lastReceipt=%v/survivor=%v", lastReceipt, survivingCapture), func(t *testing.T) {
				f := newProducerStoreFixture(t)
				oldest := passageEpoch.AddDate(0, 0, 1-persistenceModel().RawRetentionDays)
				oldPartition, _ := partitionProducerRetentionSource(t, f, oldest)
				run, err := f.store.Activate(f.ctx, persistenceModel())
				if err != nil {
					t.Fatal(err)
				}
				expectedCheckpoint, lostCapture := int64(10), int64(11)
				if lastReceipt {
					f.capture(11)
					f.exec(`UPDATE vialis.capturas_colectivos SET recibido_en=$1 WHERE id_captura=11`, oldest)
					f.processProducer(run.Model.Name)
					expectedCheckpoint, lostCapture = 11, 12
				} else {
					f.exec(`UPDATE vialis.h0012_modelos SET activado_en=$1 WHERE nombre=$2`, oldest, run.Model.Name)
				}
				f.capture(lostCapture)
				f.exec(`UPDATE vialis.capturas_colectivos SET recibido_en=$1 WHERE id_captura=$2`, oldest, lostCapture)
				f.producerRaw(lostCapture, "lost", "g", int(oldest.Sub(passageEpoch)/time.Second), 250)
				if survivingCapture {
					f.capture(lostCapture + 1)
				}
				diagnostics, modes := f.count("h0012_diagnosticos"), f.count("h0012_productores")

				clock := &producerMidnightBeginner{database: f.pool, before: passageEpoch, after: passageEpoch.AddDate(0, 0, 1), nextCaptureQuery: f.store.sql(findNextProducerCaptureSQL)}
				clock.purge = func(ctx context.Context) error {
					purge, err := f.pool.Begin(ctx)
					if err != nil {
						return err
					}
					defer rollbackBottlenecks(purge)
					if _, err = purge.Exec(ctx, "DROP TABLE "+pgx.Identifier{f.store.schema, oldPartition}.Sanitize()); err != nil {
						return err
					}
					if _, err = purge.Exec(ctx, f.store.sql(`DELETE FROM vialis.capturas_colectivos WHERE recibido_en<$1`), oldest.AddDate(0, 0, 1)); err != nil {
						return err
					}
					return purge.Commit(ctx)
				}
				store := &BottlenecksStore{database: clock, schema: f.store.schema}
				progress, err := store.ProcessNext(f.ctx, run.Model.Name)
				if !errors.Is(err, bottlenecks.ErrSourceExpired) || progress.Processed || progress.Checkpoint != 0 {
					t.Fatalf("purged source was skipped or reported idle: progress=%+v err=%v", progress, err)
				}
				if !clock.purged || !clock.pinned || len(clock.references) < 2 || !clock.references[0].Equal(oldest) || !clock.references[1].Equal(oldest) {
					t.Fatalf("original reference was not revalidated after pin: purged=%v pinned=%v refs=%v", clock.purged, clock.pinned, clock.references)
				}
				recovered, states := f.producerRecovery(run.Model.Name)
				if recovered.Checkpoint != expectedCheckpoint || len(states) != 0 || f.count("h0012_mediciones") != 0 || f.count("h0012_diagnosticos") != diagnostics || f.count("h0012_productores") != modes {
					t.Fatalf("purge failure committed partial producer progress: %+v", recovered)
				}
			})
		}
	}
}

func TestProducerStoreSourcePinIncludesPartitionsAndAllowsInsert(t *testing.T) {
	f := newProducerStoreFixture(t)
	oldest := passageEpoch.AddDate(0, 0, 1-persistenceModel().RawRetentionDays)
	oldPartition, freshPartition := partitionProducerRetentionSource(t, f, oldest)
	f.capture(11)
	f.producerRaw(11, "existing", "g", int(oldest.Sub(passageEpoch)/time.Second), 250)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackBottlenecks(tx)
	if _, err = tx.Exec(f.ctx, f.store.sql(`LOCK TABLE vialis.posiciones_colectivos_raw IN ACCESS SHARE MODE`)); err != nil {
		t.Fatal(err)
	}
	var locks int
	if err = tx.QueryRow(f.ctx, `
		SELECT count(*) FROM pg_locks l
		JOIN pg_class c ON c.oid=l.relation JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE l.pid=pg_backend_pid() AND n.nspname=$1 AND c.relname=ANY($2::text[])
			AND l.mode='AccessShareLock' AND l.granted
	`, f.store.schema, []string{"posiciones_colectivos_raw", oldPartition, freshPartition}).Scan(&locks); err != nil || locks != 3 {
		t.Fatalf("raw descendants not pinned: locks=%d err=%v", locks, err)
	}
	insertCtx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	tag, err := f.pool.Exec(insertCtx, f.store.sql(`
		INSERT INTO vialis.posiciones_colectivos_raw
		SELECT $1::date,$2::timestamptz,$2::timestamptz,'concurrent-insert',gcba_route_id,gcba_direction,
			gtfs_route_id,gtfs_direction_id,gtfs_shape_id,gtfs_shape_hash,posicion,id_captura
		FROM vialis.posiciones_colectivos_raw WHERE id_vehiculo='existing'
	`), passageEpoch.Format("2006-01-02"), passageEpoch)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("source pin blocked normal insert: rows=%d err=%v", tag.RowsAffected(), err)
	}
	other, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackBottlenecks(other)
	_, err = other.Exec(f.ctx, "LOCK TABLE "+pgx.Identifier{f.store.schema, oldPartition}.Sanitize()+" IN ACCESS EXCLUSIVE MODE NOWAIT")
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "55P03" {
		t.Fatalf("partition purge was not blocked by source pin: %v", err)
	}
}
