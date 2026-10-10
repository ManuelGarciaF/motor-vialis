-- $1: recepción de la captura; $2: id_captura.
-- La primera observación persistida por vehículo/timestamp es la canónica.
WITH targets AS MATERIALIZED (
    SELECT
        e.gcba_route_id,
        e.gcba_direction,
        e.gtfs_route_id,
        e.gtfs_direction_id,
        r.gtfs_shape_id,
        md5(encode(ST_AsEWKB(r.geom), 'hex')) AS shape_hash
    FROM (SELECT DISTINCT route_id, direction FROM realtime_positions_batch) p
    JOIN vialis.equivalencias_recorridos_gcba e
        ON e.gcba_route_id = p.route_id AND e.gcba_direction = p.direction
    JOIN vialis.recorridos r
        ON r.gtfs_route_id = e.gtfs_route_id AND r.direction_id = e.gtfs_direction_id
)
INSERT INTO vialis.posiciones_colectivos_raw (
    dia_observado,
    observado_en,
    recibido_en,
    id_vehiculo,
    gcba_route_id,
    gcba_direction,
    gtfs_route_id,
    gtfs_direction_id,
    gtfs_shape_id,
    gtfs_shape_hash,
    posicion,
    velocidad_metros_segundo,
    id_captura
)
SELECT
    p.dia,
    p.observado,
    $1,
    p.vehiculo,
    p.route_id,
    p.direction,
    e.gtfs_route_id,
    e.gtfs_direction_id,
    e.gtfs_shape_id,
    e.shape_hash,
    ST_SetSRID(ST_MakePoint(p.lon, p.lat), 4326),
    p.speed,
    $2
FROM realtime_positions_batch p
JOIN targets e ON e.gcba_route_id = p.route_id AND e.gcba_direction = p.direction
ON CONFLICT (dia_observado, id_vehiculo, observado_en) DO NOTHING;
