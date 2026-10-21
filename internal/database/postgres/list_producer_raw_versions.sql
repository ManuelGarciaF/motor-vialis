-- Receipt membership, never current mappings. $1/$2 inclusive receipt bounds;
-- $3 optional vehicle, $4 optional closed GPS watermark.
SELECT DISTINCT gtfs_route_id, gtfs_direction_id, gtfs_shape_id, gtfs_shape_hash
FROM vialis.posiciones_colectivos_raw
WHERE id_captura BETWEEN $1 AND $2
  AND ($3::text IS NULL OR id_vehiculo = $3)
  AND ($4::timestamptz IS NULL OR observado_en > $4)
ORDER BY gtfs_route_id, gtfs_direction_id, gtfs_shape_id, gtfs_shape_hash
