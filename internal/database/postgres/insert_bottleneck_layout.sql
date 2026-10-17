INSERT INTO vialis.h0012_trazados (
    modelo,
    gtfs_route_id,
    direction_id,
    gtfs_shape_id,
    gtfs_shape_hash,
    linea,
    ramal,
    longitud_metros,
    geom,
    paradas
)
VALUES (
    $1,
    $2,
    $3,
    $4,
    $5,
    $6,
    $7,
    $8,
    ST_GeomFromEWKB($9),
    '[]'
)
ON CONFLICT (
    modelo,
    gtfs_route_id,
    direction_id,
    gtfs_shape_id,
    gtfs_shape_hash
)
DO NOTHING
RETURNING id
