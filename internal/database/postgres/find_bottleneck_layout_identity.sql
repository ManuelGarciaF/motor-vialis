SELECT id
FROM vialis.h0012_trazados
WHERE modelo = $1
    AND gtfs_route_id = $2
    AND direction_id = $3
    AND gtfs_shape_id = $4
    AND gtfs_shape_hash = $5
