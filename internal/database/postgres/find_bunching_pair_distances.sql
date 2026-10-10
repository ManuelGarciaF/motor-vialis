-- $1: radio del prefilter espacial (incluye el margen de un micrómetro).
-- Incluye pares cercanos nuevos y pares abiertos aunque ya se hayan separado.
WITH candidates AS (
    SELECT a.vehiculo AS a, b.vehiculo AS b
    FROM bunching_snapshot a
    JOIN bunching_snapshot b
        ON a.vehiculo COLLATE "C" < b.vehiculo COLLATE "C"
       AND a.route_id = b.route_id
       AND a.direction = b.direction
       AND a.gtfs_route_id = b.gtfs_route_id
       AND a.gtfs_direction_id = b.gtfs_direction_id
       AND a.shape_hash = b.shape_hash
       AND ST_DWithin(a.posicion::geography, b.posicion::geography, $1)

    UNION

    SELECT e.id_vehiculo_a, e.id_vehiculo_b
    FROM vialis.aglutinaciones_colectivos e
    JOIN bunching_snapshot a ON a.vehiculo = e.id_vehiculo_a
    JOIN bunching_snapshot b ON b.vehiculo = e.id_vehiculo_b
    WHERE e.cerrado_en IS NULL
      AND a.route_id = e.gcba_route_id AND b.route_id = e.gcba_route_id
      AND a.direction = e.gcba_direction AND b.direction = e.gcba_direction
      AND a.gtfs_route_id = e.gtfs_route_id AND b.gtfs_route_id = e.gtfs_route_id
      AND a.gtfs_direction_id = e.gtfs_direction_id AND b.gtfs_direction_id = e.gtfs_direction_id
      AND a.shape_hash = e.gtfs_shape_hash AND b.shape_hash = e.gtfs_shape_hash
)
SELECT
    a.route_id,
    a.direction,
    a.gtfs_route_id,
    a.gtfs_direction_id,
    a.gtfs_shape_id,
    a.shape_hash,
    c.a,
    c.b,
    ST_Distance(a.posicion::geography, b.posicion::geography)
FROM candidates c
JOIN bunching_snapshot a ON a.vehiculo = c.a
JOIN bunching_snapshot b ON b.vehiculo = c.b;
