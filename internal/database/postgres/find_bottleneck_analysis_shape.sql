SELECT
    gtfs_route_id,
    direction_id,
    gtfs_shape_id,
    md5(encode(ST_AsEWKB(geom), 'hex')),
    ST_Length(geom::geography),
    linea,
    ramal
FROM vialis.recorridos
WHERE id_recorrido = $1
