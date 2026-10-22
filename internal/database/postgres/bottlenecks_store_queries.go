package postgres

import _ "embed"

//go:embed bottlenecks_materialize_stops.sql
var bottlenecksMaterializeStopsSQL string

//go:embed find_bottleneck_run.sql
var findBottleneckRunSQL string

//go:embed activate_bottleneck_model.sql
var activateBottleneckModelSQL string

//go:embed find_bottleneck_layout.sql
var findBottleneckLayoutSQL string

//go:embed find_bottleneck_shape_snapshot.sql
var findBottleneckShapeSnapshotSQL string

//go:embed insert_bottleneck_layout.sql
var insertBottleneckLayoutSQL string

//go:embed find_bottleneck_layout_identity.sql
var findBottleneckLayoutIdentitySQL string

//go:embed insert_bottleneck_sector.sql
var insertBottleneckSectorSQL string

//go:embed update_bottleneck_layout_stops.sql
var updateBottleneckLayoutStopsSQL string

//go:embed update_bottleneck_checkpoint.sql
var updateBottleneckCheckpointSQL string

//go:embed check_bottleneck_retention.sql
var checkBottleneckRetentionSQL string

//go:embed insert_bottleneck_measurement.sql
var insertBottleneckMeasurementSQL string
