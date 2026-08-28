-- Un recorrido con sus paradas en orden y la geometria de cada tramo.
--
-- El JOIN a recorridos_paradas es LEFT a proposito: un recorrido sin paradas
-- tiene que devolver igual su renglon, porque "existe pero no se puede simular"
-- y "no existe" son dos respuestas distintas para quien consulta.
--
-- $1 id del recorrido
SELECT
    r.id_recorrido,
    r.linea,
    r.ramal,
    r.nombre_publico,
    r.direction_id,
    COALESCE(r.destino, '') AS destino,
    COALESCE(r.descripcion, '') AS descripcion,
    r.distancia_metros,
    rp.nro_parada,
    p.gtfs_stop_id,
    p.nombre,
    COALESCE(p.codigo, '') AS codigo,
    ST_Y(p.posicion) AS latitud,
    ST_X(p.posicion) AS longitud,
    ST_AsGeoJSON(rp.tramo_hasta_siguiente) AS tramo_hasta_siguiente
FROM vialis.recorridos r
LEFT JOIN vialis.recorridos_paradas rp
    ON rp.id_recorrido = r.id_recorrido
LEFT JOIN vialis.paradas p
    ON p.id_parada = rp.id_parada
WHERE r.id_recorrido = $1::BIGINT
ORDER BY rp.nro_parada;
