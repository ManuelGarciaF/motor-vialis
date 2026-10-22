SELECT
    gtfs_route_id,
    direction_id,
    gtfs_shape_id,
    gtfs_shape_hash,
    longitud_metros
FROM vialis.bottlenecks_trazados
WHERE modelo = $1
    AND id = $2
