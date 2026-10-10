-- $1: ruta GTFS; $2: sentido; $3: hash del shape; $4: mínimo de muestras.
-- $5: límite inferior de frescura; $6: skew futuro máximo.
-- $7: duración mínima en segundos; $8: velocidad máxima mínima por unidad (m/s).
-- El orden de las columnas de evento debe coincidir con scanEvent.
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
    velocidad_maxima_b_metros_segundo
FROM vialis.aglutinaciones_colectivos
WHERE cerrado_en IS NULL
  AND gtfs_route_id = $1
  AND gtfs_direction_id = $2
  AND gtfs_shape_hash = $3
  AND observaciones >= $4
  AND EXTRACT(EPOCH FROM (ultimo_observado_en - primero_observado_en)) >= $7::double precision
  AND velocidad_maxima_a_metros_segundo >= $8
  AND velocidad_maxima_b_metros_segundo >= $8
  AND LEAST(
      COALESCE(observado_a_en, ultimo_observado_en),
      COALESCE(observado_b_en, ultimo_observado_en)
  ) >= $5
  AND GREATEST(
      COALESCE(observado_a_en, ultimo_observado_en),
      COALESCE(observado_b_en, ultimo_observado_en)
  ) <= $6
  AND COALESCE(ultima_confirmacion_en, ultimo_observado_en) >= $5
  AND EXISTS (
      SELECT 1
      FROM vialis.equivalencias_recorridos_gcba e
      WHERE e.gcba_route_id = aglutinaciones_colectivos.gcba_route_id
        AND e.gcba_direction = aglutinaciones_colectivos.gcba_direction
        AND e.gtfs_route_id = aglutinaciones_colectivos.gtfs_route_id
        AND e.gtfs_direction_id = aglutinaciones_colectivos.gtfs_direction_id
  )
ORDER BY primero_observado_en, id_evento;
