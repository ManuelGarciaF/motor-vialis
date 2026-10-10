-- $1: id_recorrido. El hash identifica la versión geométrica vigente.
SELECT
    r.gtfs_route_id,
    r.direction_id,
    r.linea,
    r.ramal,
    md5(encode(ST_AsEWKB(r.geom), 'hex')),
    EXISTS (
        SELECT 1
        FROM vialis.equivalencias_recorridos_gcba e
        WHERE e.gtfs_route_id = r.gtfs_route_id
          AND e.gtfs_direction_id = r.direction_id
    )
FROM vialis.recorridos r
WHERE r.id_recorrido = $1;
