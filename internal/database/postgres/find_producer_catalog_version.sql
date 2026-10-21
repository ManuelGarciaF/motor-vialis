SELECT id_recorrido
FROM vialis.recorridos
WHERE gtfs_route_id = $1
  AND direction_id = $2
  AND gtfs_shape_id = $3
  AND md5(encode(ST_AsEWKB(geom), 'hex')) = $4
