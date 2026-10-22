-- Exploratory only: nearest-point projection is not a trajectory map matcher.
-- No writes, no use of H0011 event eligibility. UTC [from,to); default last 24h
-- ending just after the latest retained GPS observation (not wall-clock now).
\set ON_ERROR_STOP on
\if :{?max_gap_seconds}
\else
\set max_gap_seconds 180
\endif
\if :{?max_distance_meters}
\else
\set max_distance_meters 100
\endif
\if :{?max_speed_kph}
\else
\set max_speed_kph 100
\endif
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '5min';
\if :{?to}
\else
SELECT (max(observado_en) + interval '1 microsecond')::text AS "to"
FROM vialis.posiciones_colectivos_raw \gset
\endif
\if :{?from}
\else
SELECT (:'to'::timestamptz - interval '24 hours')::text AS "from" \gset
\endif
WITH shapes AS MATERIALIZED (
    SELECT gtfs_route_id, direction_id, gtfs_shape_id,
           md5(encode(ST_AsEWKB(geom), 'hex')) AS hash,
           ST_Transform(geom, 32721) AS geom
    FROM vialis.recorridos
), raw AS MATERIALIZED (
    SELECT p.*, ST_Transform(p.posicion, 32721) AS point
    FROM vialis.posiciones_colectivos_raw p
    WHERE observado_en >= :'from'::timestamptz AND observado_en < :'to'::timestamptz
      AND dia_observado BETWEEN (:'from'::timestamptz AT TIME ZONE 'UTC')::date
                           AND (:'to'::timestamptz AT TIME ZONE 'UTC')::date
), projected AS MATERIALIZED (
    -- Micrometer rounding avoids floating-point drift at boundaries and at
    -- the half-sector remainder decision after reprojection.
    SELECT p.*, round(ST_Length(s.geom)::numeric, 6)::double precision AS length_m,
           round((ST_LineLocatePoint(s.geom, p.point) * ST_Length(s.geom))::numeric, 6)::double precision AS progress_m,
           ST_Distance(s.geom, p.point) AS distance_m
    FROM raw p LEFT JOIN shapes s ON s.gtfs_route_id = p.gtfs_route_id
      AND s.direction_id = p.gtfs_direction_id AND s.gtfs_shape_id = p.gtfs_shape_id
      AND s.hash = p.gtfs_shape_hash
), previous AS (
    SELECT *, lag(observado_en) OVER w AS prev_at,
           lag(progress_m) OVER w AS prev_progress,
           lag(distance_m) OVER w AS prev_distance,
           lag(ROW(gcba_route_id, gcba_direction, gtfs_route_id,
                   gtfs_direction_id, gtfs_shape_id, gtfs_shape_hash)::text) OVER w AS prev_scope,
           ROW(gcba_route_id, gcba_direction, gtfs_route_id,
               gtfs_direction_id, gtfs_shape_id, gtfs_shape_hash)::text AS scope
    FROM projected WINDOW w AS (PARTITION BY id_vehiculo ORDER BY observado_en)
), classified AS MATERIALIZED (
    SELECT *, extract(epoch FROM observado_en - prev_at) AS gap_s,
      coalesce(scope = prev_scope
        AND distance_m <= :max_distance_meters AND prev_distance <= :max_distance_meters
        AND observado_en > prev_at
        AND extract(epoch FROM observado_en - prev_at) <= :max_gap_seconds
        AND progress_m >= prev_progress
        AND (progress_m - prev_progress) / extract(epoch FROM observado_en - prev_at)
            <= :max_speed_kph / 3.6, false) AS valid_edge
    FROM previous
), runs AS (
    SELECT *, sum(CASE WHEN valid_edge THEN 0 ELSE 1 END) OVER
      (PARTITION BY id_vehiculo ORDER BY observado_en) AS run_id
    FROM classified
), expanded AS (
    SELECT runs.*, size_m,
      greatest(1, floor(length_m / size_m)::int +
        CASE WHEN mod(length_m::numeric, size_m) >= size_m / 2.0 THEN 1 ELSE 0 END) AS sectors
    FROM runs CROSS JOIN (VALUES (300), (500), (800)) sizes(size_m)
    WHERE valid_edge AND progress_m > prev_progress
), crossings AS (
    SELECT e.*, b.boundary, b.meters,
      prev_at + (observado_en - prev_at) *
        ((b.meters - prev_progress) / (progress_m - prev_progress)) AS crossed_at
    FROM expanded e CROSS JOIN LATERAL (
      SELECT k AS boundary, CASE WHEN k = sectors THEN length_m ELSE k * size_m END AS meters
      FROM generate_series(greatest(0, floor(prev_progress / size_m)::int),
                           least(sectors, floor(progress_m / size_m)::int + 1)) k
    ) b
    WHERE (b.meters > prev_progress OR (b.meters = 0 AND prev_progress = 0))
      AND b.meters <= progress_m
), paired AS (
    SELECT *, lead(boundary) OVER w AS next_boundary,
      lead(meters) OVER w AS next_meters, lead(crossed_at) OVER w AS exited_at,
      lead(observado_en) OVER w AS exit_edge_at
    FROM crossings WINDOW w AS
      (PARTITION BY id_vehiculo, run_id, size_m ORDER BY boundary, crossed_at)
), passages AS MATERIALIZED (
    SELECT *, extract(epoch FROM exited_at - crossed_at) AS duration_s,
      (next_meters - meters) / extract(epoch FROM exited_at - crossed_at) * 3.6 AS speed_kph
    FROM paired
    WHERE next_boundary = boundary + 1 AND exited_at > crossed_at
), summary AS (
    SELECT sizes.size_m, count(p.duration_s) AS passages,
      count(p.duration_s) FILTER (WHERE p.exit_edge_at = p.observado_en) AS same_gps_interval,
      count(DISTINCT p.id_vehiculo) AS vehicles,
      count(DISTINCT (p.gtfs_route_id, p.gtfs_direction_id, p.gtfs_shape_hash)) AS routes,
      count(DISTINCT (p.gtfs_route_id, p.gtfs_direction_id, p.gtfs_shape_hash, p.boundary)) AS sectors,
      percentile_cont(0.5) WITHIN GROUP (ORDER BY p.duration_s) AS median_duration_s,
      percentile_cont(0.5) WITHIN GROUP (ORDER BY p.speed_kph) AS median_speed_kph
    FROM (VALUES (300), (500), (800)) sizes(size_m)
    LEFT JOIN passages p USING (size_m) GROUP BY sizes.size_m
)
SELECT jsonb_pretty(jsonb_build_object(
  'from', :'from', 'to', :'to', 'projection', 'EPSG:32721',
  'parameters', jsonb_build_object('maxGapSeconds', :max_gap_seconds,
    'maxDistanceMeters', :max_distance_meters, 'maxSpeedKph', :max_speed_kph),
  'coverage', (SELECT jsonb_build_object('rawPositions', count(*),
    'matchedCurrentShape', count(length_m),
    'withinDistance', count(*) FILTER (WHERE distance_m <= :max_distance_meters),
    'vehicles', count(DISTINCT id_vehiculo)) FROM projected),
  'edges', (SELECT jsonb_build_object('valid', count(*) FILTER (WHERE valid_edge),
    'withPredecessor', count(prev_at),
    'backwards', count(*) FILTER (WHERE scope = prev_scope AND progress_m < prev_progress),
    'gapTooLong', count(*) FILTER (WHERE gap_s > :max_gap_seconds),
    'medianGapSeconds', percentile_cont(0.5) WITHIN GROUP (ORDER BY gap_s),
    'p90GapSeconds', percentile_cont(0.9) WITHIN GROUP (ORDER BY gap_s)) FROM classified),
  'segments', (SELECT jsonb_agg(to_jsonb(s) ORDER BY size_m) FROM summary s),
  'limitations', jsonb_build_array('Nearest-point projection; loops and ambiguous matching unresolved',
    'Times interpolated at constant progress within each GPS interval',
    'Same-interval passages have no independent interior timing evidence',
    'Only retained raw with current shape hash; terminal waits not excluded',
    'Counts do not estimate fleet coverage or establish recurrent bottlenecks')
));
ROLLBACK;
