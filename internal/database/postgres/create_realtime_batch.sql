CREATE TEMP TABLE realtime_positions_batch (
    dia DATE,
    observado TIMESTAMPTZ,
    vehiculo TEXT,
    route_id TEXT,
    direction SMALLINT,
    lon DOUBLE PRECISION,
    lat DOUBLE PRECISION,
    speed DOUBLE PRECISION
) ON COMMIT DROP;
