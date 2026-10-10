-- $1: última observación mínima; impide reabrir con muestras ya utilizadas.
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
WHERE cerrado_en IS NOT NULL
  AND ultimo_observado_en >= $1
ORDER BY id_evento DESC;
