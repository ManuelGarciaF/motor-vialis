-- $1 model; $2/$3 inclusive first-receipt capture bounds; $4 radius;
-- $5 optional vehicle; $6 optional finalized GPS watermark. No mapping join.
WITH raw AS MATERIALIZED (
    SELECT p.*, t.id AS layout_id
    FROM vialis.posiciones_colectivos_raw p
    JOIN vialis.h0012_trazados t
      ON t.modelo = $1
     AND t.gtfs_route_id = p.gtfs_route_id
     AND t.direction_id = p.gtfs_direction_id
     AND t.gtfs_shape_id = p.gtfs_shape_id
     AND t.gtfs_shape_hash = p.gtfs_shape_hash
    WHERE p.id_captura BETWEEN $2 AND $3
      AND ($5::text IS NULL OR p.id_vehiculo = $5)
      AND ($6::timestamptz IS NULL OR p.observado_en > $6)
), shapes AS MATERIALIZED (
    SELECT t.id, t.geom
    FROM vialis.h0012_trazados t
    WHERE t.id IN (SELECT DISTINCT layout_id FROM raw)
), pieces AS MATERIALIZED (
    SELECT s.id, (d).path[1] edge, (d).geom geom,
           ST_Length((d).geom::geography) length_m
    FROM shapes s CROSS JOIN LATERAL ST_DumpSegments(s.geom) d
), edges AS MATERIALIZED (
    SELECT *, coalesce(sum(length_m) OVER (
        PARTITION BY id ORDER BY edge
        ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING
    ), 0) start_m
    FROM pieces
)
SELECT p.id_captura, p.layout_id, p.id_vehiculo, p.gcba_route_id,
       p.gcba_direction, p.gtfs_route_id, p.gtfs_direction_id,
       p.gtfs_shape_id, p.gtfs_shape_hash, p.observado_en,
       coalesce(c.progress, ARRAY[]::double precision[]),
       coalesce(c.distances, ARRAY[]::double precision[])
FROM raw p
LEFT JOIN LATERAL (
    WITH nearby AS (
        SELECT e.*, ST_ClosestPoint(e.geom::geography, p.posicion::geography)::geometry point
        FROM edges e
        WHERE e.id = p.layout_id AND e.length_m > 0
          AND ST_DWithin(e.geom::geography, p.posicion::geography, $4)
    ), projected AS (
        SELECT edge, start_m + ST_Length(ST_LineSubstring(geom, 0,
               ST_LineLocatePoint(geom, point))::geography) progress_m,
               ST_Distance(point::geography, p.posicion::geography) distance_m
        FROM nearby
    ), neighbors AS (
        SELECT *, lag(edge) OVER w prev_edge, lead(edge) OVER w next_edge,
               lag(distance_m) OVER w prev_distance, lead(distance_m) OVER w next_distance
        FROM projected WINDOW w AS (ORDER BY edge)
    ), minima AS (
        SELECT round(progress_m::numeric, 6)::double precision progress_m,
               min(distance_m) distance_m
        FROM neighbors
        WHERE distance_m <= $4
          AND (prev_edge IS NULL OR prev_edge <> edge - 1 OR distance_m <= prev_distance)
          AND (next_edge IS NULL OR next_edge <> edge + 1 OR distance_m <= next_distance)
        GROUP BY round(progress_m::numeric, 6)
    )
    SELECT array_agg(progress_m ORDER BY progress_m) progress,
           array_agg(distance_m ORDER BY progress_m) distances
    FROM minima
) c ON true
ORDER BY p.id_vehiculo, p.observado_en
