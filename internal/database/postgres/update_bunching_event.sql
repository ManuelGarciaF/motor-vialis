-- $1..$27: los mismos argumentos que insert_bunching_event.sql; $28: id_evento.
-- Conserva la identidad y el comienzo originales del encuentro.
UPDATE vialis.aglutinaciones_colectivos
SET
    ultimo_observado_en = $10,
    observado_a_en = $11,
    observado_b_en = $12,
    cerrado_en = $13,
    motivo_cierre = $14,
    distancia_minima_metros = $15,
    ultima_distancia_metros = $16,
    ultima_posicion_a = ST_SetSRID(ST_MakePoint($17, $18), 4326),
    ultima_posicion_b = ST_SetSRID(ST_MakePoint($19, $20), 4326),
    observaciones = $21,
    capturas_faltantes = $22,
    ultima_confirmacion_en = $25,
    velocidad_maxima_a_metros_segundo = $26,
    velocidad_maxima_b_metros_segundo = $27
WHERE id_evento = $28
  AND gcba_route_id = $1
  AND gcba_direction = $2
  AND gtfs_route_id = $3
  AND gtfs_direction_id = $4
  AND gtfs_shape_id = $5
  AND gtfs_shape_hash = $6
  AND id_vehiculo_a = $7
  AND id_vehiculo_b = $8
  AND primero_observado_en = $9
  AND linea = $23
  AND ramal = $24;
