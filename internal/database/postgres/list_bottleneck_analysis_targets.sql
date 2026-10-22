-- $1/$2: GPS [from,to). Select targets via the active H0011 equivalences,
-- never by equality between API/GCBA identifiers and GTFS identifiers.
WITH observed AS MATERIALIZED (
 SELECT gcba_route_id,gcba_direction,count(*) AS positions
 FROM vialis.posiciones_colectivos_raw
 WHERE observado_en >= $1 AND observado_en < $2
 AND dia_observado BETWEEN ($1::timestamptz AT TIME ZONE 'UTC')::date
                      AND ($2::timestamptz AT TIME ZONE 'UTC')::date
 GROUP BY gcba_route_id,gcba_direction
)
SELECT r.id_recorrido,r.gtfs_route_id,r.direction_id,r.gtfs_shape_id,
 md5(encode(ST_AsEWKB(r.geom),'hex')),sum(o.positions)::bigint
FROM observed o
JOIN vialis.equivalencias_recorridos_gcba e
 ON e.gcba_route_id=o.gcba_route_id AND e.gcba_direction=o.gcba_direction
JOIN vialis.recorridos r
 ON r.gtfs_route_id=e.gtfs_route_id AND r.direction_id=e.gtfs_direction_id
GROUP BY r.id_recorrido,r.gtfs_route_id,r.direction_id,r.gtfs_shape_id,r.geom
ORDER BY r.id_recorrido;
