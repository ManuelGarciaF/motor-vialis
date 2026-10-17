SELECT
    gtfs_route_id,
    direction_id,
    gtfs_shape_id,
    gtfs_shape_hash,
    longitud_metros
FROM vialis.h0012_trazados
WHERE modelo = $1
    AND id = $2
