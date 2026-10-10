-- $1: límite inferior de frescura; $2: skew futuro máximo.
-- Usa posición y velocidad canónicas de raw para vehículo/timestamp.
CREATE TEMP TABLE bunching_snapshot ON COMMIT DROP AS
WITH targets AS MATERIALIZED (
    SELECT
        e.gcba_route_id,
        e.gcba_direction,
        e.gtfs_route_id,
        e.gtfs_direction_id,
        r.gtfs_shape_id,
        r.linea,
        r.ramal,
        md5(encode(ST_AsEWKB(r.geom), 'hex')) AS shape_hash
    FROM (SELECT DISTINCT route_id, direction FROM realtime_positions_batch) p
    JOIN vialis.equivalencias_recorridos_gcba e
        ON e.gcba_route_id = p.route_id AND e.gcba_direction = p.direction
    JOIN vialis.recorridos r
        ON r.gtfs_route_id = e.gtfs_route_id AND r.direction_id = e.gtfs_direction_id
), latest AS (
    SELECT DISTINCT ON (vehiculo COLLATE "C") *
    FROM realtime_positions_batch
    ORDER BY vehiculo COLLATE "C", observado DESC, route_id COLLATE "C", direction
)
SELECT
    p.vehiculo,
    p.observado,
    p.route_id,
    p.direction,
    e.gtfs_route_id,
    e.gtfs_direction_id,
    e.gtfs_shape_id,
    e.shape_hash,
    e.linea,
    e.ramal,
    raw.posicion,
    raw.velocidad_metros_segundo
FROM latest p
JOIN targets e
    ON e.gcba_route_id = p.route_id AND e.gcba_direction = p.direction
JOIN vialis.posiciones_colectivos_raw raw
    ON raw.dia_observado = p.dia
   AND raw.id_vehiculo = p.vehiculo
   AND raw.observado_en = p.observado
WHERE p.observado >= $1 AND p.observado <= $2;
