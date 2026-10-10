SELECT
    vehiculo,
    observado,
    route_id,
    direction,
    gtfs_route_id,
    gtfs_direction_id,
    gtfs_shape_id,
    shape_hash,
    ST_X(posicion),
    ST_Y(posicion),
    linea,
    ramal,
    velocidad_metros_segundo
FROM bunching_snapshot;
