-- $1..$6: los mismos filtros que count_bunching_history.sql.
-- $7: limit; $8: offset.
-- El orden de las columnas de evento debe coincidir con scanEvent;
-- current_line_id es la columna adicional propia de la consulta histórica.
SELECT
    id_evento,
    gcba_route_id,
    gcba_direction,
    gtfs_route_id,
    gtfs_direction_id,
    gtfs_shape_id,
    gtfs_shape_hash,
    id_vehiculo_a,
    id_vehiculo_b,
    primero_observado_en,
    ultimo_observado_en,
    COALESCE(observado_a_en, ultimo_observado_en),
    COALESCE(observado_b_en, ultimo_observado_en),
    cerrado_en,
    COALESCE(motivo_cierre, ''),
    distancia_minima_metros,
    ultima_distancia_metros,
    ST_X(ultima_posicion_a),
    ST_Y(ultima_posicion_a),
    ST_X(ultima_posicion_b),
    ST_Y(ultima_posicion_b),
    observaciones,
    capturas_faltantes,
    linea,
    ramal,
    COALESCE(ultima_confirmacion_en, ultimo_observado_en),
    velocidad_maxima_a_metros_segundo,
    velocidad_maxima_b_metros_segundo,
    (
        SELECT r.id_recorrido
        FROM vialis.recorridos r
        WHERE r.gtfs_route_id = e.gtfs_route_id
          AND r.direction_id = e.gtfs_direction_id
          AND md5(encode(ST_AsEWKB(r.geom), 'hex')) = e.gtfs_shape_hash
          AND EXISTS (
              SELECT 1
              FROM vialis.equivalencias_recorridos_gcba m
              WHERE m.gcba_route_id = e.gcba_route_id
                AND m.gcba_direction = e.gcba_direction
                AND m.gtfs_route_id = e.gtfs_route_id
                AND m.gtfs_direction_id = e.gtfs_direction_id
          )
    ) AS current_line_id
FROM vialis.aglutinaciones_colectivos e
WHERE observaciones >= $1
  AND EXTRACT(EPOCH FROM (ultimo_observado_en - primero_observado_en)) >= $5::double precision
  AND velocidad_maxima_a_metros_segundo >= $6
  AND velocidad_maxima_b_metros_segundo >= $6
  AND primero_observado_en < $2
  AND COALESCE(cerrado_en, ultimo_observado_en) >= $3
  AND (
      $4 = 'all'
      OR ($4 = 'open' AND cerrado_en IS NULL)
      OR ($4 = 'closed' AND cerrado_en IS NOT NULL)
  )
ORDER BY primero_observado_en DESC, id_evento DESC
LIMIT $7 OFFSET $8;
