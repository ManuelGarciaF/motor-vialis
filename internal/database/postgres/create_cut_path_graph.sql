-- Prepares one cut path request: the waypoints ($1, a JSON array of
-- {id, longitude, latitude} ordered by id), the local graph inside their
-- bounding box grown by $2 metres, and each waypoint snapped onto the nearest
-- street within $3 metres. A cut closes streets, it is not a trip: every edge
-- costs its length in both directions and turn restrictions are not loaded.
CREATE TEMP TABLE cut_path_waypoints ON COMMIT DROP AS
SELECT
    (point ->> 'id')::bigint AS pid,
    ST_SetSRID(ST_MakePoint(
        (point ->> 'longitude')::double precision,
        (point ->> 'latitude')::double precision
    ), 4326) AS geom
FROM jsonb_array_elements($1::jsonb) AS item(point);

CREATE TEMP TABLE cut_path_graph ON COMMIT DROP AS
WITH area AS (
    SELECT ST_Buffer(
        ST_Envelope(ST_Collect(geom))::geography,
        $2::double precision
    )::geometry AS geom
    FROM cut_path_waypoints
)
SELECT
    street.id_calle AS id,
    street.origen AS source,
    street.destino AS target,
    -- pgRouting rejects a zero cost; a degenerate edge still has to connect.
    GREATEST(ST_Length(street.geom::geography), 0.01) AS cost,
    street.geom
FROM vialis.calles street
CROSS JOIN area
WHERE street.geom && area.geom
  AND ST_Intersects(street.geom, area.geom);

-- A waypoint with no street within $3 metres keeps a NULL edge, so the caller
-- can name it.
CREATE TEMP TABLE cut_path_points ON COMMIT DROP AS
SELECT
    waypoint.pid,
    snapped.id AS edge_id,
    snapped.fraction,
    ST_LineInterpolatePoint(snapped.geom, snapped.fraction) AS snapped_geom
FROM cut_path_waypoints waypoint
LEFT JOIN LATERAL (
    SELECT
        edge.id,
        edge.geom,
        ST_LineLocatePoint(edge.geom, waypoint.geom) AS fraction
    FROM cut_path_graph edge
    WHERE edge.geom && ST_Expand(waypoint.geom, $3::double precision / 90000.0)
      AND ST_DWithin(edge.geom::geography, waypoint.geom::geography, $3::double precision)
    ORDER BY ST_Distance(edge.geom::geography, waypoint.geom::geography), edge.id
    LIMIT 1
) snapped ON true;

-- Consecutive waypoints, in order: one leg each.
CREATE TEMP TABLE cut_path_legs ON COMMIT DROP AS
SELECT pid AS from_pid, next_pid AS to_pid
FROM (
    SELECT pid, lead(pid) OVER (ORDER BY pid) AS next_pid
    FROM cut_path_waypoints
) ordered
WHERE next_pid IS NOT NULL;
