package postgres

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed list_realtime_partitions.sql
var listRealtimePartitionsSQL string

//go:embed delete_expired_realtime_captures.sql
var deleteExpiredRealtimeCapturesSQL string

//go:embed insert_realtime_capture.sql
var insertRealtimeCaptureSQL string

//go:embed create_realtime_batch.sql
var createRealtimeBatchSQL string

//go:embed count_unmapped_realtime_positions.sql
var countUnmappedRealtimePositionsSQL string

//go:embed insert_realtime_positions.sql
var insertRealtimePositionsSQL string

//go:embed check_realtime_schema.sql
var checkRealtimeSchemaSQL string

// RealtimeSchemaReady checks the operational schema required by the worker.
// Database-specific SQL stays in this package, not in the command entry point.
func RealtimeSchemaReady(ctx context.Context, database *pgxpool.Pool) (bool, error) {
	var ready bool
	err := database.QueryRow(ctx, checkRealtimeSchemaSQL, "vialis").Scan(&ready)
	return ready, err
}
