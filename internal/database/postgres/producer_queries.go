package postgres

import _ "embed"

//go:embed find_next_producer_capture.sql
var findNextProducerCaptureSQL string

//go:embed list_producer_raw_versions.sql
var listProducerRawVersionsSQL string

//go:embed find_producer_catalog_version.sql
var findProducerCatalogVersionSQL string

//go:embed prepare_producer_projection.sql
var prepareProducerProjectionSQL string

//go:embed populate_producer_projection.sql
var populateProducerProjectionSQL string

//go:embed find_producer_candidates.sql
var findProducerCandidatesSQL string

//go:embed list_producer_states.sql
var listProducerStatesSQL string

//go:embed upsert_producer_state.sql
var upsertProducerStateSQL string

//go:embed check_producer_schema.sql
var checkProducerSchemaSQL string
