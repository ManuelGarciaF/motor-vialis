package postgres

import _ "embed"

// Complete SQL statements, embedded at build time like the other repositories.
// Keep parameter and scan order documented in the SQL files; no runtime file I/O
// or SQL-template expansion is needed.

//go:embed find_bunching_line.sql
var findBunchingLineSQL string

//go:embed find_latest_bunching_capture.sql
var findLatestBunchingCaptureSQL string

//go:embed find_current_bunching.sql
var findCurrentBunchingSQL string

//go:embed count_bunching_history.sql
var countBunchingHistorySQL string

//go:embed find_bunching_history.sql
var findBunchingHistorySQL string

//go:embed find_bunching_recurrence.sql
var findBunchingRecurrenceSQL string

//go:embed create_bunching_snapshot.sql
var createBunchingSnapshotSQL string

//go:embed prepare_bunching_snapshot.sql
var prepareBunchingSnapshotSQL string

//go:embed find_bunching_snapshot_positions.sql
var findBunchingSnapshotPositionsSQL string

//go:embed find_open_bunching.sql
var findOpenBunchingSQL string

//go:embed find_bunching_pair_distances.sql
var findBunchingPairDistancesSQL string

//go:embed find_recent_closed_bunching.sql
var findRecentClosedBunchingSQL string

//go:embed insert_bunching_event.sql
var insertBunchingEventSQL string

//go:embed update_bunching_event.sql
var updateBunchingEventSQL string
